package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// fetchView is a reference target that holds nothing but can fetch.
type fetchView struct {
	fatView
	fetched string
}

func (f *fetchView) RefKind() string       { return "linear" }
func (f *fetchView) HasRef(string) bool    { return false }
func (f *fetchView) SelectRef(string) bool { return false }
func (f *fetchView) FetchRef(id, _ string) tea.Cmd {
	f.fetched = id
	return func() tea.Msg { return nil }
}

// Following a reference nobody has loaded goes to the view that can fetch
// it, and that view is asked to; the browser is only for the rest.
func TestFollowFetchesIntoTheViewThatCan(t *testing.T) {
	fv := &fetchView{fatView: fatView{title: "Linear"}}
	m := New(config.Default(), []View{&fatView{title: "PRs"}, fv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	ref := ui.Ref{Kind: "linear", ID: "SRE-1", URL: "https://linear.app/x/SRE-1"}
	if !m.resolves(ref) {
		t.Error("a fetchable reference should count as resolvable (no browser arrow)")
	}
	cmd := m.followRef(ref)
	if m.current != 1 || fv.fetched != "SRE-1" || cmd == nil {
		t.Errorf("follow: current=%d fetched=%q cmd=%v", m.current, fv.fetched, cmd != nil)
	}
	if cmd := m.followRef(ui.Ref{Kind: "pr", ID: "x", URL: "https://github.com/o/r/pull/1"}); cmd == nil {
		t.Error("a reference with no view to take it should still open the browser")
	}
}

// relatedView has a focused pane with a foldable related section and one
// reference to follow.
type relatedView struct {
	fatView
	expanded bool
}

func (r *relatedView) PaneFocused() bool   { return true }
func (r *relatedView) FocusPane(bool) bool { return false }
func (r *relatedView) PaneScrolls() bool   { return true }
func (r *relatedView) Refs() []ui.Ref {
	return []ui.Ref{{Kind: "pr", ID: "o/r#5", URL: "https://github.com/o/r/pull/5"}}
}
func (r *relatedView) ExpandRelated() bool {
	if r.expanded {
		return false
	}
	r.expanded = true
	return true
}

// In a focused pane the first 'l' expands the related section and the
// second raises the picker over it, so a float never needs the list.
func TestFollowExpandsThenPicks(t *testing.T) {
	v := &relatedView{}
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 120, 40, true
	m.layout()
	got, _ := m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	m = got.(Model)
	if !v.expanded || m.picker != nil {
		t.Fatalf("first l: expanded=%v picker=%v, want the section expanded and no picker", v.expanded, m.picker != nil)
	}
	got, _ = m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	m = got.(Model)
	if m.picker == nil {
		t.Fatal("second l did not raise the picker")
	}
}
