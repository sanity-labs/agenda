package linear

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

type teamLabel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	IsGroup bool   `json:"isGroup"`
	Parent  *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"parent"`
}

// title is the label as the menu shows it: children carry their group, so
// same-named children of different groups can be told apart.
func (l teamLabel) title() string {
	if l.Parent != nil {
		return l.Parent.Name + " / " + l.Name
	}
	return l.Name
}

// isOn reports whether the issue carries l. Rows cached before labels
// carried ids fall back to the name.
func isOn(nodes []label, l teamLabel) bool {
	return slices.ContainsFunc(nodes, func(x label) bool { return x.ID == l.ID || x.ID == "" && x.Name == l.Name })
}

type labelSetMsg struct {
	ident string
	lbl   teamLabel
	on    bool
	err   error
}

type commentAddedMsg struct {
	ident string
	err   error
}

const commentMutation = `mutation($input: CommentCreateInput!) { commentCreate(input: $input) { success } }`

// Team.labels leaves out workspace labels, so ask for every label whose
// team is this one or none. Groups come back too: they are the parents of
// the create flow and are not applied themselves.
const labelsQuery = `query($team: ID!, $after: String) {
  issueLabels(first: 250, after: $after, filter: { or: [{ team: { null: true } }, { team: { id: { eq: $team } } }] }) {
    nodes { id name color isGroup parent { id name } }
    pageInfo { hasNextPage endCursor }
  }
}`

const labelCreateMutation = `mutation($input: IssueLabelCreateInput!) { issueLabelCreate(input: $input) { success issueLabel { id name color } } }`

func (v *View) fetchLabels(team string) []teamLabel {
	v.teamMu.Lock()
	c, ok := v.labelCache[team]
	v.teamMu.Unlock()
	if ok {
		return c
	}
	var all []teamLabel
	var after any
	for range 20 {
		var out struct {
			IssueLabels struct {
				Nodes    []teamLabel
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		if err := v.post(labelsQuery, map[string]any{"team": team, "after": after}, &out); err != nil {
			return all
		}
		all = append(all, out.IssueLabels.Nodes...)
		if !out.IssueLabels.PageInfo.HasNextPage {
			break
		}
		after = out.IssueLabels.PageInfo.EndCursor
	}
	slices.SortStableFunc(all, func(a, b teamLabel) int {
		return strings.Compare(strings.ToLower(a.title()), strings.ToLower(b.title()))
	})
	v.teamMu.Lock()
	if v.labelCache == nil {
		v.labelCache = map[string][]teamLabel{}
	}
	v.labelCache[team] = all
	v.teamMu.Unlock()
	return all
}

// labelsMenu lists the labels that can be applied, the ones on the issue
// marked; picking one toggles it. A query that matches none offers to
// create it.
func (v *View) labelsMenu(sel issue, st issueStates) *ui.Menu {
	m := &ui.Menu{Title: "Labels", Hint: "Type to filter, or a new label's name"}
	for _, l := range st.labels {
		if l.IsGroup {
			continue
		}
		on := isOn(sel.Labels.Nodes, l)
		title := l.title()
		if on {
			title += " (on)"
		}
		m.Options = append(m.Options, ui.Command{ID: "label:" + l.ID, Title: title, Run: func() tea.Cmd {
			return v.setLabel(sel.Identifier, l, !on)
		}})
	}
	m.Fallback = func(name string) []ui.Command {
		return []ui.Command{{ID: "label-create", Title: `Create label "` + name + `"`, Next: func() *ui.Menu {
			return v.groupMenu(sel.Identifier, st, name)
		}}}
	}
	return m
}

// groupMenu picks the group a new label goes under, or none.
func (v *View) groupMenu(ident string, st issueStates, name string) *ui.Menu {
	m := &ui.Menu{Title: "New label in", Options: []ui.Command{{ID: "group:none", Title: "No group", Run: func() tea.Cmd {
		return v.createLabel(ident, st.teamID, nil, name)
	}}}}
	for _, g := range st.labels {
		if g.IsGroup {
			m.Options = append(m.Options, ui.Command{ID: "group:" + g.ID, Title: g.title(), Run: func() tea.Cmd {
				return v.createLabel(ident, st.teamID, &g, name)
			}})
		}
	}
	return m
}

// createLabel makes the label on the team, then puts it on the issue.
func (v *View) createLabel(ident, team string, group *teamLabel, name string) tea.Cmd {
	return func() tea.Msg {
		input := map[string]any{"name": name, "teamId": team}
		if group != nil {
			input["parentId"] = group.ID
		}
		var out struct {
			IssueLabelCreate struct {
				Success    bool
				IssueLabel teamLabel
			}
		}
		err := v.post(labelCreateMutation, map[string]any{"input": input}, &out)
		if err == nil && !out.IssueLabelCreate.Success {
			err = fmt.Errorf("linear: issueLabelCreate was not successful")
		}
		if err != nil {
			return labelSetMsg{ident: ident, lbl: teamLabel{Name: name}, err: err}
		}
		l := out.IssueLabelCreate.IssueLabel
		if group != nil {
			l.Parent = &struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{group.ID, group.Name}
		}
		return v.setLabel(ident, l, true)()
	}
}

// setLabel sends only the label that changed, so a label added elsewhere
// meanwhile is not dropped the way a full labelIds replacement would.
func (v *View) setLabel(ident string, l teamLabel, on bool) tea.Cmd {
	return func() tea.Msg {
		key := "removedLabelIds"
		if on {
			key = "addedLabelIds"
		}
		var out struct{ IssueUpdate struct{ Success bool } }
		err := v.post(stateMutation, map[string]any{"id": ident, "input": map[string]any{key: []string{l.ID}}}, &out)
		if err == nil && !out.IssueUpdate.Success {
			err = fmt.Errorf("linear: issueUpdate was not successful")
		}
		return labelSetMsg{ident: ident, lbl: l, on: on, err: err}
	}
}

func (v *View) updateLabel(msg labelSetMsg) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg {
			return ui.Status(ui.SeverityError, "Linear", "could not change labels of "+msg.ident, msg.err.Error())
		}
	}
	for i := range v.raw {
		if v.raw[i].Identifier != msg.ident {
			continue
		}
		nodes := slices.DeleteFunc(slices.Clone(v.raw[i].Labels.Nodes), func(x label) bool { return isOn([]label{x}, msg.lbl) })
		if msg.on {
			nodes = append(nodes, label{ID: msg.lbl.ID, Name: msg.lbl.Name, Color: msg.lbl.Color})
		}
		v.raw[i].Labels.Nodes = nodes
	}
	// A label just created is not in the menu's list yet.
	if st, ok := v.states[msg.ident]; ok && !slices.ContainsFunc(st.labels, func(x teamLabel) bool { return x.ID == msg.lbl.ID }) {
		st.labels = append(slices.Clone(st.labels), msg.lbl)
		v.states[msg.ident] = st
		v.teamMu.Lock()
		if c, ok := v.labelCache[st.teamID]; ok && !slices.ContainsFunc(c, func(x teamLabel) bool { return x.ID == msg.lbl.ID }) {
			v.labelCache[st.teamID] = append(slices.Clone(c), msg.lbl)
		}
		v.teamMu.Unlock()
	}
	v.applySort()
	body := msg.ident + " lost " + msg.lbl.title()
	if msg.on {
		body = msg.ident + " now has " + msg.lbl.title()
	}
	return func() tea.Msg { return ui.ToastMsg{Title: "Linear", Body: body} }
}

func (v *View) addComment(ident, body string) tea.Cmd {
	return func() tea.Msg {
		var out struct{ CommentCreate struct{ Success bool } }
		err := v.post(commentMutation, map[string]any{"input": map[string]any{"issueId": ident, "body": body}}, &out)
		if err == nil && !out.CommentCreate.Success {
			err = fmt.Errorf("linear: commentCreate was not successful")
		}
		return commentAddedMsg{ident: ident, err: err}
	}
}

func (v *View) updateCommentAdded(msg commentAddedMsg) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg {
			return ui.Status(ui.SeverityError, "Linear", "could not comment on "+msg.ident, msg.err.Error())
		}
	}
	delete(v.comments, msg.ident)
	v.commentsRev++
	for i := range v.raw {
		if v.raw[i].Identifier == msg.ident {
			v.raw[i].Comments.Nodes = append(v.raw[i].Comments.Nodes, struct {
				ID string `json:"id"`
			}{})
		}
	}
	v.applySort()
	return tea.Batch(v.maybeFetchComments(), func() tea.Msg {
		return ui.ToastMsg{Title: "Linear", Body: "commented on " + msg.ident}
	})
}

type favoriteAddedMsg struct {
	src navSource
	err error
}

const favoriteMutation = `mutation($input: FavoriteCreateInput!) { favoriteCreate(input: $input) { success } }`

// addFavorite pins the project or initiative the list is showing; the
// favourites section reads Linear's own favorites, so it shows up on refetch.
func (v *View) addFavorite(src navSource) tea.Cmd {
	return func() tea.Msg {
		var out struct{ FavoriteCreate struct{ Success bool } }
		err := v.post(favoriteMutation, map[string]any{"input": map[string]any{src.Kind + "Id": src.ProjectID}}, &out)
		if err == nil && !out.FavoriteCreate.Success {
			err = fmt.Errorf("linear: favoriteCreate was not successful")
		}
		return favoriteAddedMsg{src: src, err: err}
	}
}

type member struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type assignedMsg struct {
	ident string
	to    member // zero ID: unassigned
	err   error
}

const membersQuery = `query($team: String!, $after: String) {
  team(id: $team) {
    members(first: 100, after: $after, filter: { active: { eq: true } }) {
      nodes { id displayName }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

func (v *View) fetchMembers(team string) []member {
	v.teamMu.Lock()
	c, ok := v.memberCache[team]
	v.teamMu.Unlock()
	if ok {
		return c
	}
	var all []member
	var after any
	for range 20 {
		var out struct {
			Team struct {
				Members struct {
					Nodes    []member
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
				}
			}
		}
		if err := v.post(membersQuery, map[string]any{"team": team, "after": after}, &out); err != nil {
			return all
		}
		all = append(all, out.Team.Members.Nodes...)
		if !out.Team.Members.PageInfo.HasNextPage {
			break
		}
		after = out.Team.Members.PageInfo.EndCursor
	}
	v.teamMu.Lock()
	if v.memberCache == nil {
		v.memberCache = map[string][]member{}
	}
	v.memberCache[team] = all
	v.teamMu.Unlock()
	return all
}

// assignMenu lists the members fetched with the commands. Me comes first; the current assignee
// is marked and, like the Unassign row, clears the assignment.
func (v *View) assignMenu(sel issue, st issueStates) *ui.Menu {
	m := &ui.Menu{Title: "Assign"}
	// The live value wins: list rows can be stale or cached without the id.
	cur := st.assigneeID
	if cur == "" {
		cur = sel.Assignee.ID
	}
	if cur != "" {
		m.Options = append(m.Options, ui.Command{ID: "assign:none", Title: "Unassign", Run: func() tea.Cmd {
			return v.setAssignee(sel.Identifier, member{})
		}})
	}
	members := slices.Clone(st.members)
	slices.SortStableFunc(members, func(a, b member) int {
		if a.ID == st.viewerID || b.ID == st.viewerID {
			return map[bool]int{true: -1, false: 1}[a.ID == st.viewerID]
		}
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	for _, u := range members {
		title := u.DisplayName
		if u.ID == st.viewerID {
			title = "Me (" + title + ")"
		}
		to := u
		if u.ID == cur {
			title += " (assigned)"
			to = member{}
		}
		m.Options = append(m.Options, ui.Command{ID: "assign:" + u.ID, Title: title, Run: func() tea.Cmd {
			return v.setAssignee(sel.Identifier, to)
		}})
	}
	return m
}

func (v *View) setAssignee(ident string, to member) tea.Cmd {
	return func() tea.Msg {
		var id any // null unassigns
		if to.ID != "" {
			id = to.ID
		}
		var out struct{ IssueUpdate struct{ Success bool } }
		err := v.post(stateMutation, map[string]any{"id": ident, "input": map[string]any{"assigneeId": id}}, &out)
		if err == nil && !out.IssueUpdate.Success {
			err = fmt.Errorf("linear: issueUpdate was not successful")
		}
		return assignedMsg{ident: ident, to: to, err: err}
	}
}

func (v *View) updateAssigned(msg assignedMsg) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg {
			return ui.Status(ui.SeverityError, "Linear", "could not assign "+msg.ident, msg.err.Error())
		}
	}
	for i := range v.raw {
		if v.raw[i].Identifier == msg.ident {
			v.raw[i].Assignee.ID, v.raw[i].Assignee.DisplayName = msg.to.ID, msg.to.DisplayName
		}
	}
	if st, ok := v.states[msg.ident]; ok {
		st.assigneeID = msg.to.ID
		v.states[msg.ident] = st
	}
	v.applySort()
	body := msg.ident + " is now unassigned"
	if msg.to.ID != "" {
		body = msg.ident + " assigned to " + msg.to.DisplayName
	}
	return func() tea.Msg { return ui.ToastMsg{Title: "Linear", Body: body} }
}
