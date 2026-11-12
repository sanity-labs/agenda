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

// issueStates is what the palette commands need: the issue's UUID, the
// workflow states of its team, and whether the viewer is subscribed.
type issueStates struct {
	id         string
	states     []workflowState
	subscribed bool
}

const statesQuery = `query($id: String!) {
  viewer { id }
  issue(id: $id) { id subscribers(first: 250) { nodes { id } } team { states(first: 50) { nodes { id name type color position } } } }
}`

const subscribeMutation = `mutation($id: String!) { issueSubscribe(id: $id) { success } }`
const unsubscribeMutation = `mutation($id: String!) { issueUnsubscribe(id: $id) { success } }`

const stateMutation = `mutation($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success }
}`

type statesMsg struct {
	ident string
	st    issueStates
	err   error
}

type subscribedMsg struct {
	ident string
	on    bool
	err   error
}

type prioritySetMsg struct {
	ident string
	to    int
	err   error
}

var priorityNames = []string{"No priority", "Urgent", "High", "Medium", "Low"}

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
			Viewer struct{ ID string }
			Issue  struct {
				ID          string
				Subscribers struct{ Nodes []struct{ ID string } }
				Team        struct {
					States struct{ Nodes []workflowState }
				}
			}
		}
		err := v.post(statesQuery, map[string]any{"id": ident}, &out)
		st := issueStates{id: out.Issue.ID, states: out.Issue.Team.States.Nodes}
		for _, u := range out.Issue.Subscribers.Nodes {
			st.subscribed = st.subscribed || u.ID == out.Viewer.ID
		}
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

// Commands implements ui.Commander: Status (opens the team's workflow
// states), Link related issue, then subscribe or unsubscribe.
func (v *View) Commands() []ui.Command {
	sel := v.list.Selected()
	detail := sel.Identifier + " " + sel.Title
	var cmds []ui.Command
	if st, ok := v.states[sel.Identifier]; ok {
		cmds = append(cmds, ui.Command{
			ID: "status", Title: "Status", Detail: detail, Aliases: []string{"state", "move"},
			Next: func() *ui.Menu { return v.statusMenu(sel, st) },
		})
	}
	if sel.Identifier != "" && v.token != "" {
		cmds = append(cmds, ui.Command{
			ID: "priority", Title: "Priority", Detail: priorityNames[sel.Priority%len(priorityNames)] + "  " + detail,
			Aliases: []string{"prio", "p1", "p2", "p3", "p4"},
			Next:    func() *ui.Menu { return v.priorityMenu(sel) },
		})
		cmds = append(cmds, ui.Command{
			ID: "link", Title: "Link related issue", Detail: detail,
			Aliases: []string{"mark", "relate", "relation", "block", "duplicate"},
			Next:    func() *ui.Menu { return v.relationMenu(sel.Identifier) },
		})
	}
	if st, ok := v.states[sel.Identifier]; ok {
		title := "Subscribe to issue"
		if st.subscribed {
			title = "Unsubscribe from issue"
		}
		cmds = append(cmds, ui.Command{
			ID: "subscribe", Title: title, Detail: sel.Identifier + " " + sel.Title,
			Run: func() tea.Cmd { return v.setSubscribed(sel.Identifier, st.id, !st.subscribed) },
		})
	}
	return cmds
}

func (v *View) statusMenu(sel issue, st issueStates) *ui.Menu {
	m := &ui.Menu{Title: "Status"}
	for _, s := range st.states {
		title, run := s.Name, func() tea.Cmd { return v.setState(sel.Identifier, st.id, s) }
		if s.Name == sel.State.Name {
			title, run = s.Name+" (current)", func() tea.Cmd { return nil }
		}
		m.Options = append(m.Options, ui.Command{ID: "status:" + s.ID, Title: title, Run: run})
	}
	return m
}

func (v *View) priorityMenu(sel issue) *ui.Menu {
	m := &ui.Menu{Title: "Priority"}
	for p, name := range priorityNames {
		title, run := name, func() tea.Cmd { return v.setPriority(sel.Identifier, p) }
		if p == sel.Priority {
			title, run = name+" (current)", func() tea.Cmd { return nil }
		}
		m.Options = append(m.Options, ui.Command{ID: "priority:" + name, Title: title, Run: run})
	}
	return m
}

func (v *View) setPriority(ident string, to int) tea.Cmd {
	return func() tea.Msg {
		var out struct{ IssueUpdate struct{ Success bool } }
		err := v.post(stateMutation, map[string]any{"id": ident, "input": map[string]any{"priority": to}}, &out)
		if err == nil && !out.IssueUpdate.Success {
			err = fmt.Errorf("linear: issueUpdate was not successful")
		}
		return prioritySetMsg{ident: ident, to: to, err: err}
	}
}

func (v *View) setSubscribed(ident, id string, on bool) tea.Cmd {
	return func() tea.Msg {
		q, field := unsubscribeMutation, "issueUnsubscribe"
		if on {
			q, field = subscribeMutation, "issueSubscribe"
		}
		var out map[string]struct{ Success bool }
		err := v.post(q, map[string]any{"id": id}, &out)
		if err == nil && !out[field].Success {
			err = fmt.Errorf("linear: %s was not successful", field)
		}
		return subscribedMsg{ident: ident, on: on, err: err}
	}
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
	case subscribedMsg:
		if msg.err != nil {
			return func() tea.Msg {
				return ui.Status(ui.SeverityError, "Linear", "could not change subscription to "+msg.ident, msg.err.Error())
			}, true
		}
		st := v.states[msg.ident]
		st.subscribed = msg.on
		v.states[msg.ident] = st
		body := "unsubscribed from " + msg.ident
		if msg.on {
			body = "subscribed to " + msg.ident
		}
		return tea.Batch(
			func() tea.Msg { return ui.CommandsChangedMsg{} },
			func() tea.Msg { return ui.ToastMsg{Title: "Linear", Body: body} },
		), true
	case prioritySetMsg:
		if msg.err != nil {
			return func() tea.Msg {
				return ui.Status(ui.SeverityError, "Linear", "could not change priority of "+msg.ident, msg.err.Error())
			}, true
		}
		for i := range v.raw {
			if v.raw[i].Identifier == msg.ident {
				v.raw[i].Priority, v.raw[i].PriorityLabel = msg.to, priorityNames[msg.to]
			}
		}
		v.applySort()
		return func() tea.Msg {
			return ui.ToastMsg{Title: "Linear", Body: msg.ident + " priority is now " + priorityNames[msg.to]}
		}, true
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
