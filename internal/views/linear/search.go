package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

const searchQuery = `query($term: String!, $first: Int!) {
  searchIssues(term: $term, first: $first) {
    nodes {` + issueFields + `}
  }
}`

// Search implements ui.Searcher over every issue the token can see, not
// just the current source.
func (v *View) Search(query, scope string, limit int) tea.Cmd {
	token, mode, rev := v.token, v.sort, v.rev
	return func() tea.Msg {
		res := ui.SearchResultsMsg{Query: query, Scope: scope}
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
		return res
	}
}
