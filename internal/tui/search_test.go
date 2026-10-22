package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// searchView answers every search with one repo scope and one PR it holds.
type searchView struct {
	jumpView
	scopes []string
}

func (s *searchView) RefKind() string { return "pr" }
func (s *searchView) Search(query, scope string, _ int) tea.Cmd {
	s.scopes = append(s.scopes, scope)
	return func() tea.Msg {
		return ui.SearchResultsMsg{Query: query, Scope: scope, Results: []ui.Ref{
			{Kind: ui.ScopeKind, ID: "o/r", Label: "o/r"},
			{Kind: "pr", ID: s.id, Label: "a PR", URL: s.id},
		}}
	}
}

func hit(m Model, k tea.KeyPressMsg) (Model, tea.Cmd) {
	got, cmd := m.Update(k)
	return got.(Model), cmd
}

// Picking a repo narrows the search to it; picking a PR jumps to it in the
// view that answered, like following a cross-reference.
func TestQuickSearchNarrowsThenJumps(t *testing.T) {
	sv := &searchView{jumpView: jumpView{fatView: fatView{title: "PRs"}, id: "u1"}}
	m := New(config.Default(), []View{&fatView{title: "Sessions"}, sv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m.current = 1
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.search == nil {
		t.Fatal("ctrl+k did not open the search box")
	}
	m, _ = hit(m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	m.takeSearchResults(sv.Search("o", "", 5)().(ui.SearchResultsMsg))

	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.search == nil || m.search.Scope != "o/r" || m.search.Query != "" {
		t.Fatalf("picking a repo should narrow, got %+v", m.search)
	}
	m.search.Query = "1"
	m.takeSearchResults(sv.Search("1", "o/r", 5)().(ui.SearchResultsMsg))
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m.current = 0
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.search != nil || m.current != 1 {
		t.Errorf("picking a PR should close the box and jump: search=%v current=%d", m.search != nil, m.current)
	}
}

// Clicking a result picks it the way enter would, even with the cursor on
// another row.
func TestQuickSearchClickPicksResult(t *testing.T) {
	sv := &searchView{jumpView: jumpView{fatView: fatView{title: "PRs"}, id: "u1"}}
	m := New(config.Default(), []View{&fatView{title: "Sessions"}, sv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m.current = 1
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m, _ = hit(m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	m.takeSearchResults(sv.Search("o", "", 5)().(ui.SearchResultsMsg))

	box := m.search.View()
	bx, by := m.centerOf(box)
	row := -1
	for i, l := range strings.Split(box, "\n") {
		if strings.Contains(l, "a PR") {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("the PR result is not in the box")
	}
	m.current = 0
	got, _ := m.click(bx+6, by+row)
	m = got.(Model)
	if m.search != nil || m.current != 1 {
		t.Errorf("clicking the PR should close the box and jump: search=%v current=%d", m.search != nil, m.current)
	}
}
