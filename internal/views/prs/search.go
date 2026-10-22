package prs

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/store"
	"github.com/sanity-labs/agenda/internal/ui"
)

// searchNode is an issueOrPullRequest or search hit: a PR decodes in full,
// an issue fills only the fields its fragment asks for.
type searchNode struct {
	Typename string `json:"__typename"`
	pr
}

const issueFragment = `... on Issue { number title url state repository { nameWithOwner } }`

// Search implements ui.Searcher. A number is looked up in every repo the
// tab has loaded (or only the scoped one), along with open items whose
// number starts with it; words name a repo to narrow to, or, once
// narrowed, search that repo's issues and PRs.
func (v *View) Search(query, scope string, limit int) tea.Cmd {
	typed := query
	query = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "#"))
	num, numErr := strconv.Atoi(query)
	repos := v.knownRepos()
	if scope != "" {
		repos = []string{scope}
	}
	var loaded []pr
	for _, set := range [][]pr{v.raw, v.reviewRaw, v.opened} {
		loaded = append(loaded, set...)
	}
	mode, rev := v.sort, v.rev
	return func() tea.Msg {
		res := ui.SearchResultsMsg{Query: typed, Scope: scope}
		var nodes []searchNode
		switch {
		case numErr == nil && num > 0:
			nodes, res.Err = numberMatches(query, repos, scope, loaded, limit)
		case scope != "":
			nodes, res.Err = searchRepo(scope, query, limit)
		default:
			res.Results, res.Err = repoRefs(query, repos, limit)
			return res
		}
		exact := 0
		if numErr == nil {
			exact = num
		}
		for _, n := range sortNodes(nodes, exact, mode, rev, limit) {
			res.Results = append(res.Results, n.ref())
		}
		return res
	}
}

// numberMatches is the exact #num in each repo, then open items whose
// number has it as a prefix: the scoped repo's from GitHub, the rest from
// what the tab already holds. Only limit prefix matches are fetched, newest
// PRs before issues, so a one-digit prefix stays one small request.
func numberMatches(prefix string, repos []string, scope string, loaded []pr, limit int) ([]searchNode, error) {
	num, _ := strconv.Atoi(prefix)
	lookups := make([]numberRef, 0, len(repos))
	for _, r := range repos {
		lookups = append(lookups, numberRef{r, num})
	}
	if scope != "" {
		open, err := openNumbers(scope)
		if err != nil {
			return nil, err
		}
		for _, n := range open {
			if len(lookups) >= len(repos)+limit {
				break
			}
			if n != num && strings.HasPrefix(strconv.Itoa(n), prefix) {
				lookups = append(lookups, numberRef{scope, n})
			}
		}
	}
	nodes, err := lookupNumbers(lookups)
	if scope == "" {
		for _, p := range loaded {
			if p.Number != num && strings.HasPrefix(strconv.Itoa(p.Number), prefix) &&
				!slices.ContainsFunc(nodes, func(n searchNode) bool { return n.URL == p.URL }) {
				nodes = append(nodes, searchNode{Typename: "PullRequest", pr: p})
			}
		}
	}
	return nodes, err
}

// sortNodes puts items numbered exact first, then PRs as the list sorts
// them, then issues, and caps the lot at limit.
func sortNodes(nodes []searchNode, exact int, mode sortMode, rev bool, limit int) []searchNode {
	var hits, issues []searchNode
	var prs []pr
	for _, n := range nodes {
		switch {
		case exact > 0 && n.Number == exact:
			hits = append(hits, n)
		case n.Typename == "PullRequest":
			prs = append(prs, n.pr)
		default:
			issues = append(issues, n)
		}
	}
	for _, p := range sortPRs(prs, mode, rev) {
		hits = append(hits, searchNode{Typename: "PullRequest", pr: p})
	}
	hits = append(hits, issues...)
	return hits[:min(limit, len(hits))]
}
func (n searchNode) ref() ui.Ref {
	if n.Typename == "PullRequest" {
		return ui.PRRef(record(n.pr), n.repo(), n.Number, n.Title, n.URL)
	}
	glyph := ui.Green.Render(ui.IconGHIssue)
	if n.State == "CLOSED" {
		glyph = ui.Magenta.Render(ui.IconGHIssue)
	}
	return ui.Ref{Kind: "issue", ID: n.URL, URL: n.URL,
		Label:  glyph + "  " + ui.Truncate(n.Title, 60),
		Detail: fmt.Sprintf("%s#%d · issue", n.repo(), n.Number)}
}

// scopeRefs offers the repos whose name contains every word typed; a full
// owner/name the tab has not seen is offered as typed.
func scopeRefs(query string, repos []string) []ui.Ref {
	words := strings.Fields(strings.ToLower(query))
	var refs []ui.Ref
	for _, r := range repos {
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(strings.ToLower(r), w) }) {
			refs = append(refs, scopeRef(r))
		}
	}
	if q := strings.TrimSpace(query); strings.Count(q, "/") == 1 && !strings.ContainsAny(q, " \t") &&
		!slices.Contains(repos, q) {
		refs = append(refs, scopeRef(q))
	}
	return refs
}

func scopeRef(repo string) ui.Ref {
	return ui.Ref{Kind: ui.ScopeKind, ID: repo, Label: ui.IconTabPRs + "  " + repo, Detail: "search in this repo"}
}

func (v *View) knownRepos() []string {
	var repos []string
	for _, set := range [][]pr{v.raw, v.reviewRaw, v.opened} {
		for _, p := range set {
			if r := p.repo(); r != "" && !slices.Contains(repos, r) {
				repos = append(repos, r)
			}
		}
	}
	slices.Sort(repos)
	return repos
}

type numberRef struct {
	repo string
	num  int
}

// lookupNumbers fetches each repo's issue or PR by number in one request.
// Repo names travel as variables, never spliced into the query text.
func lookupNumbers(refs []numberRef) ([]searchNode, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	var decl, body strings.Builder
	args := []string{"api", "graphql"}
	for i, r := range refs {
		owner, name, ok := strings.Cut(r.repo, "/")
		if !ok {
			continue
		}
		if decl.Len() > 0 {
			decl.WriteString(", ")
		}
		fmt.Fprintf(&decl, "$o%d: String!, $n%d: String!, $i%d: Int!", i, i, i)
		fmt.Fprintf(&body, "r%d: repository(owner: $o%d, name: $n%d) { issueOrPullRequest(number: $i%d) { __typename ...prFields %s } }\n", i, i, i, i, issueFragment)
		args = append(args, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name), "-F", fmt.Sprintf("i%d=%d", i, r.num))
	}
	if decl.Len() == 0 {
		return nil, nil
	}
	q := "query(" + decl.String() + ") {\n" + body.String() + "}\n" + prFragment
	var out struct {
		Data map[string]*struct {
			Node *searchNode `json:"issueOrPullRequest"`
		} `json:"data"`
	}
	if err := ghGraphQL(append(args, "-f", "query="+q), &out); err != nil {
		return nil, err
	}
	var nodes []searchNode
	for i := range refs {
		if r := out.Data["r"+strconv.Itoa(i)]; r != nil && r.Node != nil {
			nodes = append(nodes, *r.Node)
		}
	}
	return nodes, nil
}

// openNumbers lists the numbers of a repo's open PRs and issues, newest
// first. ponytail: 100 of each; page if a repo outgrows that.
func openNumbers(repo string) ([]int, error) {
	owner, name, _ := strings.Cut(repo, "/")
	q := `query($o: String!, $n: String!) { repository(owner: $o, name: $n) {
  pullRequests(states: OPEN, first: 100, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { number } }
  issues(states: OPEN, first: 100, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { number } } } }`
	type nums struct {
		Nodes []struct {
			Number int `json:"number"`
		} `json:"nodes"`
	}
	var out struct {
		Data struct {
			Repository *struct {
				PullRequests nums `json:"pullRequests"`
				Issues       nums `json:"issues"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := ghGraphQL([]string{"api", "graphql", "-f", "query=" + q, "-f", "o=" + owner, "-f", "n=" + name}, &out); err != nil || out.Data.Repository == nil {
		return nil, err
	}
	var ns []int
	for _, set := range []nums{out.Data.Repository.PullRequests, out.Data.Repository.Issues} {
		for _, n := range set.Nodes {
			ns = append(ns, n.Number)
		}
	}
	return ns, nil
}

func searchRepo(repo, text string, limit int) ([]searchNode, error) {
	q := `query($q: String!, $first: Int!) { search(query: $q, type: ISSUE, first: $first) { nodes { __typename ...prFields ` +
		issueFragment + ` } } }` + prFragment
	var out struct {
		Data struct {
			Search struct {
				Nodes []searchNode `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	err := ghGraphQL([]string{"api", "graphql", "-f", "query=" + q, "-f", "q=repo:" + repo + " " + text, "-F", "first=" + strconv.Itoa(limit)}, &out)
	return out.Data.Search.Nodes, err
}

// repoRefs offers loaded repos matching the words, then GitHub's repo
// search over the same owners, so a repo with no PR of yours still shows.
func repoRefs(query string, known []string, limit int) ([]ui.Ref, error) {
	refs := scopeRefs(query, known)
	var owners []string
	for _, r := range known {
		if o, _, _ := strings.Cut(r, "/"); !slices.Contains(owners, o) {
			owners = append(owners, o)
		}
	}
	if len(refs) < limit && len(owners) > 0 {
		q := query + " in:name"
		for _, o := range owners {
			q += " org:" + o
		}
		var out struct {
			Data struct {
				Search struct {
					Nodes []struct {
						Name string `json:"nameWithOwner"`
					} `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
		}
		err := ghGraphQL([]string{"api", "graphql", "-f", "q=" + q, "-F", "first=" + strconv.Itoa(limit),
			"-f", "query=query($q: String!, $first: Int!) { search(query: $q, type: REPOSITORY, first: $first) { nodes { ... on Repository { nameWithOwner } } } }"}, &out)
		if err != nil {
			return refs[:min(limit, len(refs))], err
		}
		for _, n := range out.Data.Search.Nodes {
			if !slices.ContainsFunc(refs, func(r ui.Ref) bool { return r.ID == n.Name }) {
				refs = append(refs, scopeRef(n.Name))
			}
		}
	}
	return refs[:min(limit, len(refs))], nil
}

// ghGraphQL runs a query and decodes whatever data came back.
func ghGraphQL(args []string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stdout, err := exec.CommandContext(ctx, "gh", args...).Output()
	return decodeGraphQL(stdout, err, out)
}

// decodeGraphQL fails only when no data came back. A repo with no such
// number is a NOT_FOUND error beside the others' data, and gh exits
// non-zero for it, which must not sink the rest.
func decodeGraphQL(stdout []byte, err error, out any) error {
	var body struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if jerr := json.Unmarshal(stdout, &body); jerr != nil {
		if err != nil {
			return cmdErr(err)
		}
		return fmt.Errorf("parsing gh output: %w", jerr)
	}
	if len(body.Data) == 0 || string(body.Data) == "null" {
		switch {
		case len(body.Errors) > 0:
			return fmt.Errorf("github: %s", body.Errors[0].Message)
		case err != nil: // a REST-style 401 or 403 body
			return cmdErr(err)
		}
	}
	return json.Unmarshal(stdout, out)
}

// prFetchedMsg carries a PR brought in for a jump, or the failure to.
type prFetchedMsg struct {
	from *View
	url  string
	pr   pr
	err  error
}

// FetchRef brings a PR the tab does not list into its OPENED section, so a
// jump to it lands here rather than in a browser. One at a time: the next
// jump replaces it.
func (v *View) FetchRef(id, url string) tea.Cmd {
	q := `query($url: URI!) { resource(url: $url) { ...prFields } }` + prFragment
	return func() tea.Msg {
		var out struct {
			Data struct {
				Resource *pr `json:"resource"`
			} `json:"data"`
		}
		err := ghGraphQL([]string{"api", "graphql", "-f", "query=" + q, "-f", "url=" + id}, &out)
		msg := prFetchedMsg{from: v, url: url, err: err}
		if out.Data.Resource != nil {
			msg.pr = *out.Data.Resource
		}
		return msg
	}
}

func (v *View) takeFetched(msg prFetchedMsg) tea.Cmd {
	if msg.err != nil || msg.pr.URL == "" {
		return ui.OpenURL(msg.url)
	}
	v.opened = []pr{msg.pr}
	v.applySort()
	v.list.Select(matchURL(msg.pr.URL))
	v.annIdx = 0
	v.resetToggles()
	return tea.Batch(v.scheduleSettle(), v.resolveLinear())
}

func record(p pr) store.PR {
	return store.PR{
		URL:          p.URL,
		Repo:         p.repo(),
		Number:       p.Number,
		Title:        p.Title,
		State:        prState(p),
		CI:           ciState(p),
		Review:       reviewState(p),
		HasConflicts: p.Mergeable == "CONFLICTING",
		UpdatedAt:    p.UpdatedAt,
	}
}
