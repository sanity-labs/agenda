package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// Issue comments for the preview pane: fetched lazily per issue while the
// comments toggle ('c') is on, cached for the session, and rendered
// chronologically under the description.

type issueComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      struct {
		DisplayName string `json:"displayName"`
	} `json:"user"`
}

// commentsState tracks one issue's comment fetch.
type commentsState struct {
	list []issueComment
	err  error
	done bool
}

type commentsMsg struct {
	id       string
	comments []issueComment
	err      error
}

const commentsQuery = `query($id: String!) {
  issue(id: $id) {
    comments(first: 50) {
      nodes { body createdAt user { displayName } }
    }
  }
}`

// maybeFetchComments starts a comment fetch for the selected issue when the
// toggle is on and nothing is cached or in flight.
func (v *View) maybeFetchComments() tea.Cmd {
	if !v.showComments {
		return nil
	}
	sel := v.list.Selected()
	if sel.Identifier == "" {
		return nil
	}
	if _, started := v.comments[sel.Identifier]; started {
		return nil
	}
	if v.comments == nil {
		v.comments = map[string]*commentsState{}
	}
	v.comments[sel.Identifier] = &commentsState{} // in flight
	id, token := sel.Identifier, v.token
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]any{
			"query":     commentsQuery,
			"variables": map[string]any{"id": id},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return commentsMsg{id: id, err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return commentsMsg{id: id, err: err}
		}
		defer resp.Body.Close()

		var out struct {
			Data struct {
				Issue struct {
					Comments struct {
						Nodes []issueComment `json:"nodes"`
					} `json:"comments"`
				} `json:"issue"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return commentsMsg{id: id, err: err}
		}
		if len(out.Errors) > 0 {
			return commentsMsg{id: id, err: fmt.Errorf("linear: %s", out.Errors[0].Message)}
		}
		nodes := out.Data.Issue.Comments.Nodes
		sort.SliceStable(nodes, func(a, b int) bool { return nodes[a].CreatedAt.Before(nodes[b].CreatedAt) })
		return commentsMsg{id: id, comments: nodes}
	}
}

// renderComments renders the body of the preview's comments section for
// the selected issue, each comment as author · age over its markdown body.
// The section header is the preview's, shared with the description.
func (v *View) renderComments(id string, width int) string {
	var b strings.Builder
	st, ok := v.comments[id]
	switch {
	case !ok || !st.done:
		b.WriteString(ui.Faint.Render("  Loading comments…"))
		return b.String()
	case st.err != nil:
		b.WriteString(ui.Red.Render("  comments: " + st.err.Error()))
		return b.String()
	case len(st.list) == 0:
		b.WriteString(ui.Faint.Render("  none yet"))
		return b.String()
	}

	for i, c := range st.list {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ui.Cyan.Render("@" + c.User.DisplayName))
		b.WriteString(ui.Dim.Render(" · " + ui.Age(c.CreatedAt)))
		b.WriteByte('\n')
		b.WriteString(ui.Markdown(c.Body, width))
		b.WriteByte('\n')
	}
	return b.String()
}
