package linear

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

const relationMutation = `mutation($input: IssueRelationCreateInput!) {
  issueRelationCreate(input: $input) { success }
}`

// relationKinds are the choices of the second level. Linear stores one
// direction per type, so the reverse wordings swap the two issues.
var relationKinds = []struct {
	title, api string
	swap       bool
}{
	{"blocks", "blocks", false},
	{"blocked by", "blocks", true},
	{"related to", "related", false},
	{"duplicate of", "duplicate", false},
	{"duplicated by", "duplicate", true},
}

type exactMsg struct {
	i   issue
	err error
}

type relationMsg struct {
	ident, target, title string
	err                  error
}

// relationMenu is the relation type, then a search for the other issue.
func (v *View) relationMenu(ident string) *ui.Menu {
	m := &ui.Menu{Title: "Link related issue"}
	for _, k := range relationKinds {
		m.Options = append(m.Options, ui.Command{ID: "link:" + k.title, Title: k.title, Next: func() *ui.Menu {
			return &ui.Menu{
				Title: k.title, Hint: "Type an identifier or a title",
				Search: func(q string, limit int) tea.Cmd { return v.searchOthers(ident, q, limit) },
				Pick:   func(r ui.Ref) tea.Cmd { return v.createRelation(ident, r.ID, k.title, k.api, k.swap) },
			}
		}})
	}
	return m
}

var identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)

// searchOthers is Search without the issue being linked. A typed identifier
// is looked up exactly and put first: Linear's search is fuzzy and may not
// return it.
func (v *View) searchOthers(ident, q string, limit int) tea.Cmd {
	search := v.Search(q, "", limit+1)
	var exact tea.Cmd
	if id := strings.ToUpper(strings.TrimSpace(q)); identRe.MatchString(id) {
		exact = v.fetchIssue(id, func(i issue, err error) tea.Msg { return exactMsg{i, err} })
	}
	return func() tea.Msg {
		res, _ := search().(ui.SearchResultsMsg)
		if exact != nil {
			if e, _ := exact().(exactMsg); e.err == nil && e.i.Identifier != "" {
				ref := ui.IssueRef(e.i.Identifier, e.i.Title, e.i.URL, ui.IssueStatusIcon(e.i.State.Type, e.i.State.Name))
				res.Results = append([]ui.Ref{ref}, slices.DeleteFunc(res.Results, func(r ui.Ref) bool { return r.ID == ref.ID })...)
			}
		}
		kept := res.Results[:0]
		for _, r := range res.Results {
			if r.ID != ident && len(kept) < limit {
				kept = append(kept, r)
			}
		}
		res.Results = kept
		return res
	}
}

func relationInput(ident, target, api string, swap bool) map[string]any {
	from, to := ident, target
	if swap {
		from, to = to, from
	}
	return map[string]any{"issueId": from, "relatedIssueId": to, "type": api}
}

func (v *View) createRelation(ident, target, title, api string, swap bool) tea.Cmd {
	return func() tea.Msg {
		var out struct{ IssueRelationCreate struct{ Success bool } }
		err := v.post(relationMutation, map[string]any{"input": relationInput(ident, target, api, swap)}, &out)
		if err == nil && !out.IssueRelationCreate.Success {
			err = fmt.Errorf("linear: issueRelationCreate was not successful")
		}
		return relationMsg{ident: ident, target: target, title: title, err: err}
	}
}

func (v *View) updateRelation(msg relationMsg) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg {
			return ui.Status(ui.SeverityError, "Linear", "could not link "+msg.ident+" to "+msg.target, msg.err.Error())
		}
	}
	// Both ends changed; the other refetches when it is next selected.
	delete(v.related, msg.ident)
	delete(v.related, msg.target)
	v.relatedRev++
	return tea.Batch(v.maybeFetchRelated(), func() tea.Msg {
		return ui.ToastMsg{Title: "Linear", Body: msg.ident + " " + msg.title + " " + msg.target}
	})
}
