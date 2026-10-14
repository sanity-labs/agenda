package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"
)

// relatedState is one issue's related issues, in flight until done.
type relatedState struct {
	rel  []related
	err  error
	done bool
}

// relatedMsg carries one issue's relations, or the failure to get them.
type relatedMsg struct {
	id    string
	issue issue
	err   error
}

const relatedQuery = `query($id: String!) {
  issue(id: $id) { identifier` + relatedFields + `
  }
}`

// relatedFor is what the detail and the picker list for an issue: the
// fetched set when it has landed, else whatever the issue itself carries
// (one brought in by identifier has its relations already).
func (v *View) relatedFor(i issue) []related {
	if st, ok := v.related[i.Identifier]; ok && st.done && st.err == nil {
		return st.rel
	}
	return i.relatedIssues()
}

// maybeFetchRelated fetches the selected issue's relations, once per
// issue. Comments wait for the pane to open; relations are in the detail
// and behind 'l', so they come with the selection.
func (v *View) maybeFetchRelated() tea.Cmd {
	sel := v.list.Selected()
	if sel.Identifier == "" || v.token == "" {
		return nil
	}
	if _, started := v.related[sel.Identifier]; started {
		return nil
	}
	if v.related == nil {
		v.related = map[string]*relatedState{}
	}
	v.related[sel.Identifier] = &relatedState{} // in flight
	id, token := sel.Identifier, v.token
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]any{"query": relatedQuery, "variables": map[string]any{"id": id}})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return relatedMsg{id: id, err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return relatedMsg{id: id, err: err}
		}
		defer resp.Body.Close()
		var out struct {
			Data struct {
				Issue issue `json:"issue"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return relatedMsg{id: id, err: err}
		}
		if len(out.Errors) > 0 {
			return relatedMsg{id: id, err: fmt.Errorf("linear: %s", out.Errors[0].Message)}
		}
		return relatedMsg{id: id, issue: out.Data.Issue}
	}
}
