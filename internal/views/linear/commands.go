package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

type workflowState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

// issueStates is what a status change needs: the issue's UUID and the
// workflow states of its team.
type issueStates struct {
	id     string
	states []workflowState
}

const statesQuery = `query($id: String!) {
  issue(id: $id) { id team { states(first: 50) { nodes { id name type color position } } } }
}`

const stateMutation = `mutation($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success }
}`

type statesMsg struct {
	ident string
	st    issueStates
	err   error
}

type stateSetMsg struct {
	ident string
	to    workflowState
	err   error
}

// post sends one GraphQL request and decodes its data into out.
func (v *View) post(query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", v.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var res struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		return fmt.Errorf("linear: %s", res.Errors[0].Message)
	}
	return json.Unmarshal(res.Data, out)
}

// PrepareCommands fetches the selected issue's team states once.
func (v *View) PrepareCommands() tea.Cmd {
	ident := v.list.Selected().Identifier
	if _, ok := v.states[ident]; ident == "" || ok {
		return nil
	}
	return func() tea.Msg {
		var out struct {
			Issue struct {
				ID   string
				Team struct {
					States struct{ Nodes []workflowState }
				}
			}
		}
		err := v.post(statesQuery, map[string]any{"id": ident}, &out)
		st := issueStates{id: out.Issue.ID, states: out.Issue.Team.States.Nodes}
		slices.SortFunc(st.states, func(a, b workflowState) int {
			switch {
			case a.Position < b.Position:
				return -1
			case a.Position > b.Position:
				return 1
			}
			return 0
		})
		return statesMsg{ident: ident, st: st, err: err}
	}
}

// Commands implements ui.Commander: one "Status: X" per workflow state of
// the selected issue's team, bar the one it is in.
func (v *View) Commands() []ui.Command {
	sel := v.list.Selected()
	var cmds []ui.Command
	for _, s := range v.states[sel.Identifier].states {
		if s.Name == sel.State.Name {
			continue
		}
		cmds = append(cmds, ui.Command{
			ID: "status:" + s.ID, Title: "Status: " + s.Name, Detail: sel.Identifier + " " + sel.Title,
			Run: func() tea.Cmd { return v.setState(sel.Identifier, v.states[sel.Identifier].id, s) },
		})
	}
	return cmds
}

func (v *View) setState(ident, id string, to workflowState) tea.Cmd {
	return func() tea.Msg {
		var out struct{ IssueUpdate struct{ Success bool } }
		err := v.post(stateMutation, map[string]any{"id": id, "input": map[string]any{"stateId": to.ID}}, &out)
		if err == nil && !out.IssueUpdate.Success {
			err = fmt.Errorf("linear: issueUpdate was not successful")
		}
		return stateSetMsg{ident: ident, to: to, err: err}
	}
}

func (v *View) updateStates(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case statesMsg:
		if msg.err != nil || msg.st.id == "" {
			return func() tea.Msg {
				return ui.Status(ui.SeverityWarn, "Linear", "could not load workflow states for "+msg.ident, fmt.Sprint(msg.err))
			}, true
		}
		if v.states == nil {
			v.states = map[string]issueStates{}
		}
		v.states[msg.ident] = msg.st
		return func() tea.Msg { return ui.CommandsChangedMsg{} }, true
	case stateSetMsg:
		if msg.err != nil {
			return func() tea.Msg {
				return ui.Status(ui.SeverityError, "Linear", "could not change status of "+msg.ident, msg.err.Error())
			}, true
		}
		for i := range v.raw {
			if v.raw[i].Identifier == msg.ident {
				v.raw[i].State.Name, v.raw[i].State.Type, v.raw[i].State.Color = msg.to.Name, msg.to.Type, msg.to.Color
			}
		}
		v.applySort()
		return func() tea.Msg {
			return ui.ToastMsg{Title: "Linear", Body: msg.ident + " is now " + msg.to.Name}
		}, true
	}
	return nil, false
}
