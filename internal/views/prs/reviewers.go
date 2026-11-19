package prs

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// candidate is someone or something a review can be requested from: a user
// (login, name) or a team (login is org/slug, team set).
type candidate struct {
	login, name string
	team        bool
}

// candidates are a repo's requestable reviewers, fetched whole once: the
// largest org repo is a few hundred users, three GraphQL pages, so matching
// locally beats a query per keystroke.
type candidates struct{ users, teams []candidate }

const assignableQuery = `query($o: String!, $n: String!, $after: String) {
  repository(owner: $o, name: $n) {
    assignableUsers(first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { login name } }
  }
}`

const suggestedQuery = `query($o: String!, $n: String!, $num: Int!) {
  repository(owner: $o, name: $n) { pullRequest(number: $num) { suggestedReviewers { reviewer { login } } } }
}`

func fetchUsers(owner, name string) ([]candidate, error) {
	var all []candidate
	after := ""
	for range 10 {
		args := []string{"api", "graphql", "-f", "query=" + assignableQuery, "-f", "o=" + owner, "-f", "n=" + name}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		var out struct {
			Data struct {
				Repository struct {
					AssignableUsers struct {
						PageInfo struct {
							HasNextPage bool
							EndCursor   string
						}
						Nodes []struct{ Login, Name string }
					}
				}
			}
		}
		if err := ghGraphQL(args, &out); err != nil {
			return nil, err
		}
		page := out.Data.Repository.AssignableUsers
		for _, u := range page.Nodes {
			all = append(all, candidate{login: u.Login, name: u.Name})
		}
		if !page.PageInfo.HasNextPage {
			break
		}
		after = page.PageInfo.EndCursor
	}
	return all, nil
}

// fetchTeams lists the teams with access, as org/slug. It needs more than
// read access on some repos, so callers treat a failure as "no teams".
func fetchTeams(owner, name string) ([]candidate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "api", "--paginate", "repos/"+owner+"/"+name+"/teams",
		"--jq", ".[] | {slug, name}").Output()
	if err != nil {
		return nil, err
	}
	var all []candidate
	for dec := json.NewDecoder(bytes.NewReader(out)); ; {
		var t struct{ Slug, Name string }
		if dec.Decode(&t) != nil {
			break
		}
		all = append(all, candidate{login: owner + "/" + t.Slug, name: t.Name, team: true})
	}
	return all, nil
}

// prepareReviewers warms the candidates of the PR's repo and GitHub's
// suggestions for the PR. A failed fetch is forgotten so the next open
// retries; a failed team list only drops the teams, with a log line.
func (v *View) prepareReviewers(p pr) tea.Cmd {
	repo := p.repo()
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil
	}
	v.candMu.Lock()
	defer v.candMu.Unlock()
	if v.cands == nil {
		v.cands, v.suggested = map[string]*candidates{}, map[string][]string{}
	}
	var cmds []tea.Cmd
	changed := func() tea.Msg { return ui.CommandsChangedMsg{} }
	if _, have := v.cands[repo]; !have {
		v.cands[repo] = &candidates{}
		cmds = append(cmds, func() tea.Msg {
			users, err := fetchUsers(owner, name)
			if err != nil {
				v.candMu.Lock()
				delete(v.cands, repo)
				v.candMu.Unlock()
				return ui.Status(ui.SeverityWarn, "GitHub", "could not list reviewers of "+repo, err.Error())
			}
			teams, terr := fetchTeams(owner, name)
			v.candMu.Lock()
			v.cands[repo] = &candidates{users: users, teams: teams}
			v.candMu.Unlock()
			if terr != nil {
				return tea.BatchMsg{changed, func() tea.Msg {
					return ui.Status(ui.SeverityWarn, "GitHub", "teams of "+repo+" not listed, offering users only", terr.Error())
				}}
			}
			return changed()
		})
	}
	if _, have := v.suggested[p.URL]; !have {
		v.suggested[p.URL] = nil
		cmds = append(cmds, func() tea.Msg {
			var out struct {
				Data struct {
					Repository struct {
						PullRequest struct {
							SuggestedReviewers []struct{ Reviewer struct{ Login string } }
						}
					}
				}
			}
			err := ghGraphQL([]string{"api", "graphql", "-f", "query=" + suggestedQuery,
				"-f", "o=" + owner, "-f", "n=" + name, "-F", "num=" + strconv.Itoa(p.Number)}, &out)
			v.candMu.Lock()
			defer v.candMu.Unlock()
			if err != nil {
				delete(v.suggested, p.URL)
				return nil // suggestions are a nicety
			}
			var logins []string
			for _, s := range out.Data.Repository.PullRequest.SuggestedReviewers {
				logins = append(logins, s.Reviewer.Login)
			}
			v.suggested[p.URL] = logins
			return changed()
		})
	}
	return tea.Batch(cmds...)
}

// score ranks one candidate against the typed words: each word must hit
// login, name or team slug, the best field counting. Zero is no match.
func (c candidate) score(words []string) int {
	total := 0
	for _, w := range words {
		best := 0
		for i, f := range []string{c.login, c.name} {
			f = strings.ToLower(f)
			s := 0
			switch {
			case f == w:
				s = 100
			case strings.HasPrefix(f, w):
				s = 80
			case slices.ContainsFunc(strings.FieldsFunc(f, func(r rune) bool { return r == ' ' || r == '-' || r == '/' || r == '_' || r == '.' }),
				func(p string) bool { return strings.HasPrefix(p, w) }):
				s = 60
			case strings.Contains(f, w):
				s = 40
			case ui.FuzzyMatch(f, w):
				s = 20
			}
			if s > 0 && i == 0 {
				s += 5 // a login hit beats the same hit in a name
			}
			best = max(best, s)
		}
		if best == 0 {
			return 0
		}
		total += best
	}
	return total
}

func (c candidate) command(run func(who string) tea.Cmd) ui.Command {
	title := c.login
	if c.team {
		title = ui.Glyph(ui.IconTeam, "") + title
	}
	if c.name != "" {
		title += " · " + c.name
	}
	return ui.Command{ID: "reviewer:" + c.login, Title: title, Run: func() tea.Cmd { return run(c.login) }}
}

// reviewerMenu is the "Add reviewer" level: the candidates are read on every
// keystroke, so it fills in when the fetch lands. Typing something that
// matches nobody still offers to request exactly that.
func (v *View) reviewerMenu(p pr) *ui.Menu {
	add := func(who string) tea.Cmd {
		v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number}
		return v.submitGh("requested "+who+" on "+p.repo()+"#"+strconv.Itoa(p.Number),
			"pr", "edit", strconv.Itoa(p.Number), "-R", p.repo(), "--add-reviewer", who)
	}
	return &ui.Menu{
		Title: "Add reviewer", Hint: "Type a login, name or team",
		Match: func(q string) []ui.Command { return v.reviewerRows(p, q, add) },
		Fallback: func(q string) []ui.Command {
			who := strings.TrimPrefix(strings.TrimSpace(q), "@")
			return []ui.Command{{ID: "reviewer:" + who, Title: `Request "` + who + `"`, Run: func() tea.Cmd { return add(who) }}}
		},
	}
}

func (v *View) reviewerRows(p pr, q string, add func(string) tea.Cmd) []ui.Command {
	v.candMu.Lock()
	var pool []candidate
	if c := v.cands[p.repo()]; c != nil {
		pool = append(slices.Clone(c.users), c.teams...)
	}
	suggested := slices.Clone(v.suggested[p.URL])
	v.candMu.Unlock()

	skip := map[string]bool{strings.ToLower(p.Author.Login): true}
	for _, m := range p.reviewers() {
		skip[strings.ToLower(m[1:])] = true
	}
	pool = slices.DeleteFunc(pool, func(c candidate) bool { return skip[strings.ToLower(c.login)] })

	rank := func(c candidate) int { return slices.Index(suggested, c.login) }
	if words := strings.Fields(strings.ToLower(q)); len(words) > 0 {
		type scored struct {
			c candidate
			s int
		}
		var hits []scored
		for _, c := range pool {
			if s := c.score(words); s > 0 {
				hits = append(hits, scored{c, s})
			}
		}
		slices.SortStableFunc(hits, func(a, b scored) int { return b.s - a.s })
		var out []ui.Command
		for _, h := range hits {
			out = append(out, h.c.command(add))
		}
		return out
	}
	// Empty query: GitHub's suggestions in its order, then users, then teams.
	slices.SortStableFunc(pool, func(a, b candidate) int {
		ra, rb := rank(a), rank(b)
		switch {
		case ra >= 0 && rb >= 0:
			return ra - rb
		case ra >= 0:
			return -1
		case rb >= 0:
			return 1
		case a.team != b.team:
			if a.team {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.login), strings.ToLower(b.login))
	})
	out := make([]ui.Command, len(pool))
	for i, c := range pool {
		out[i] = c.command(add)
		if rank(c) >= 0 {
			out[i].Title += " (suggested)"
		}
	}
	return out
}
