package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

const searchQuery = `query($term: String!, $first: Int!) {
  searchIssues(term: $term, first: $first) {
    nodes {` + issueFields + `}
  }
  projects(first: 3, filter: { name: { containsIgnoreCase: $term } }) { nodes { id name } }
  initiatives(first: 3, filter: { name: { containsIgnoreCase: $term } }) { nodes { id name } }
}`

// Search implements ui.Searcher over every issue the token can see, not
// just the current source.
func (v *View) Search(query, scope string, limit int) tea.Cmd {
	token, mode, rev := v.token, v.sort, v.rev
	return func() tea.Msg {
		res := ui.SearchResultsMsg{Query: query, Scope: scope}
		if token == "" {
			return res
		}
		body, _ := json.Marshal(map[string]any{"query": searchQuery, "variables": map[string]any{"term": query, "first": limit}})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			res.Err = err
			return res
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			res.Err = err
			return res
		}
		defer resp.Body.Close()
		var out struct {
			Data struct {
				SearchIssues struct {
					Nodes []issue `json:"nodes"`
				} `json:"searchIssues"`
				Projects struct {
					Nodes []navProject `json:"nodes"`
				} `json:"projects"`
				Initiatives struct {
					Nodes []navProject `json:"nodes"`
				} `json:"initiatives"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			res.Err = err
			return res
		}
		if len(out.Errors) > 0 {
			res.Err = fmt.Errorf("linear: %s", out.Errors[0].Message)
			return res
		}
		for _, i := range sortIssues(out.Data.SearchIssues.Nodes, mode, rev) {
			res.Results = append(res.Results, ui.IssueRef(i.Identifier, i.Title, i.URL, ui.IssueStatusIcon(i.State.Type, i.State.Name)))
		}
		for _, p := range out.Data.Initiatives.Nodes {
			res.Results = append(res.Results, scopeRef("initiative", p))
		}
		for _, p := range out.Data.Projects.Nodes {
			res.Results = append(res.Results, scopeRef("project", p))
		}
		return res
	}
}

// A project or initiative result is a Linear ref whose id carries what
// FetchRef needs to make it the list's source: kind:id:name.
func scopeRef(kind string, p navProject) ui.Ref {
	icon, detail := ui.IconSearchProject, "Project"
	if kind == "initiative" {
		icon, detail = ui.IconNavInitiative, "Initiative"
	}
	return ui.Ref{Kind: "linear", ID: kind + ":" + p.ID + ":" + p.Name, Label: ui.Glyph(icon, "") + p.Name, Detail: detail}
}

func parseScopeRef(id string) (navSource, bool) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || parts[0] != "project" && parts[0] != "initiative" {
		return navSource{}, false
	}
	return navSource{Kind: parts[0], ProjectID: parts[1], Label: parts[2]}, true
}
