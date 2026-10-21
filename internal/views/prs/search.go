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
// tab has loaded (or only the scoped one); words name a repo to narrow to,
// or, once narrowed, search that repo's issues and PRs.
func (v *View) Search(query, scope string) tea.Cmd {
	typed := query
	query = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "#"))
	num, numErr := strconv.Atoi(query)
	repos := v.knownRepos()
	if scope != "" {
		repos = []string{scope}
	}
	return func() tea.Msg {
		res := ui.SearchResultsMsg{Query: typed, Scope: scope}
		var nodes []searchNode
		switch {
		case numErr == nil && num > 0:
			nodes, res.Err = lookupNumber(num, repos)
		case scope != "":
			nodes, res.Err = searchRepo(scope, query)
		default:
			res.Results = scopeRefs(query, repos)
			return res
		}
		for _, n := range nodes {
			res.Results = append(res.Results, n.ref())
		}
		return res
	}
}

func (n searchNode) ref() ui.Ref {
	if n.Typename == "PullRequest" {
		return ui.PRRef(record(n.pr), n.repo(), n.Number, n.Title, n.URL)
	}
	glyph := ui.Green.Render(ui.IconIssue)
	if n.State == "CLOSED" {
		glyph = ui.Magenta.Render(ui.IconIssue)
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
			refs = append(refs, ui.Ref{Kind: ui.ScopeKind, ID: r, Label: ui.IconTabPRs + "  " + r, Detail: "search in this repo"})
		}
	}
	if q := strings.TrimSpace(query); strings.Count(q, "/") == 1 && !strings.ContainsAny(q, " \t") &&
		!slices.Contains(repos, q) {
		refs = append(refs, ui.Ref{Kind: ui.ScopeKind, ID: q, Label: ui.IconTabPRs + "  " + q, Detail: "search in this repo"})
	}
	return refs
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

// lookupNumber asks every repo for its issue or PR #num in one request.
// Repo names travel as variables, never spliced into the query text.
func lookupNumber(num int, repos []string) ([]searchNode, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	var decl, body strings.Builder
	args := []string{"api", "graphql", "-F", "num=" + strconv.Itoa(num)}
	decl.WriteString("$num: Int!")
	for i, r := range repos {
		owner, name, ok := strings.Cut(r, "/")
		if !ok {
			continue
		}
		fmt.Fprintf(&decl, ", $o%d: String!, $n%d: String!", i, i)
		fmt.Fprintf(&body, "r%d: repository(owner: $o%d, name: $n%d) { issueOrPullRequest(number: $num) { __typename ...prFields %s } }\n", i, i, i, issueFragment)
		args = append(args, "-f", fmt.Sprintf("o%d=%s", i, owner), "-f", fmt.Sprintf("n%d=%s", i, name))
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
	for i := range repos {
		if r := out.Data["r"+strconv.Itoa(i)]; r != nil && r.Node != nil {
			nodes = append(nodes, *r.Node)
		}
	}
	return nodes, nil
}

func searchRepo(repo, text string) ([]searchNode, error) {
	q := `query($q: String!) { search(query: $q, type: ISSUE, first: 10) { nodes { __typename ...prFields ` +
		issueFragment + ` } } }` + prFragment
	var out struct {
		Data struct {
			Search struct {
				Nodes []searchNode `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	err := ghGraphQL([]string{"api", "graphql", "-f", "query=" + q, "-f", "q=repo:" + repo + " " + text}, &out)
	return out.Data.Search.Nodes, err
}

// ghGraphQL runs a query and decodes whatever data came back. A repo with
// no such number is a NOT_FOUND error next to the others' data, and gh
// exits non-zero for it, so only an unreadable body is a failure.
func ghGraphQL(args []string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stdout, err := exec.CommandContext(ctx, "gh", args...).Output()
	if jerr := json.Unmarshal(stdout, out); jerr != nil {
		if err != nil {
			return cmdErr(err)
		}
		return fmt.Errorf("parsing gh output: %w", jerr)
	}
	return nil
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
