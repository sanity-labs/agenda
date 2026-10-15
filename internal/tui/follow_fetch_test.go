package tui

import (
	"strings"
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

// Refs of different kinds are separated in the picker, each group after
// the first under its own label.
func TestPickerSeparatesKinds(t *testing.T) {
	m := New(config.Default(), []View{&fatView{title: "x"}})
	items, aligned := m.pickerItems([]ui.Ref{
		{Kind: "pr", ID: "p", Label: "a PR", URL: "u"},
		{Kind: "linear", ID: "SRE-1", Label: "SRE-1", URL: "u"},
		{Kind: "linear", ID: "SRE-2", Label: "SRE-2", URL: "u"},
		{Kind: "session", ID: "s", Label: "a session", URL: "u"},
	})
	var got []string
	for _, it := range items {
		if it.Separator {
			got = append(got, "["+it.Label+"]")
		} else {
			got = append(got, strings.TrimSuffix(it.Label, "  ↗")) // the stub resolves nothing
		}
	}
	want := "[pull requests],a PR,[issues],SRE-1,SRE-2,[sessions],a session"
	if strings.Join(got, ",") != want {
		t.Fatalf("picker = %s, want %s", strings.Join(got, ","), want)
	}
	if len(aligned) != len(items) {
		t.Fatal("aligned refs do not line up with the items")
	}
	// One kind alone: no heading over a list that needs none.
	items, _ = m.pickerItems([]ui.Ref{{Kind: "linear", ID: "SRE-1", Label: "SRE-1", URL: "u"}})
	if len(items) != 1 || items[0].Separator {
		t.Fatalf("a single-kind picker got a heading: %+v", items)
	}
}

// jumpView holds one ref and reports whether a jump gave its pane focus.
type jumpView struct {
	fatView
	id      string
	focused bool
}

func (j *jumpView) RefKind() string          { return "linear" }
func (j *jumpView) HasRef(id string) bool    { return id == j.id }
func (j *jumpView) SelectRef(id string) bool { return id == j.id }
func (j *jumpView) PaneFocused() bool        { return j.focused }
func (j *jumpView) FocusPane(on bool) bool   { j.focused = on; return true }
func (j *jumpView) PaneScrolls() bool        { return true }

// Following a ref from a list opens the target's detail the way 'v'
// would, so the jump shows the issue rather than only moving the cursor
// behind a list; from an open float the float stays and takes the keys.
func TestFollowRevealsTheDetail(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	target := &jumpView{fatView: fatView{title: "Linear"}, id: "SRE-1"}
	m := New(cfg, []View{&fatView{title: "PRs"}, target})
	m.width, m.height, m.ready = 120, 40, true
	m.layout()
	if m.floating() {
		t.Fatal("a float is open before anything happened")
	}
	cmd := m.followRef(ui.Ref{Kind: "linear", ID: "SRE-1", URL: "u"})
	if m.current != 1 {
		t.Fatal("the jump did not switch to the target view")
	}
	if !m.floating() || !target.focused {
		t.Fatalf("after the jump: floating=%v focused=%v, want the detail open with the keys", m.floating(), target.focused)
	}
	if cmd == nil {
		t.Fatal("the reveal sent no preview messages")
	}
	// Already floating (a jump from inside a detail): stays open, focus moves.
	target.focused = false
	m.followRef(ui.Ref{Kind: "linear", ID: "SRE-1", URL: "u"})
	if !m.floating() || !target.focused {
		t.Fatal("a jump from an open float lost the float or the focus")
	}
}
