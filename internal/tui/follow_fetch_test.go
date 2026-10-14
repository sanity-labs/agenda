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
	want := "a PR,[issues],SRE-1,SRE-2,[sessions],a session"
	if strings.Join(got, ",") != want {
		t.Fatalf("picker = %s, want %s", strings.Join(got, ","), want)
	}
	if len(aligned) != len(items) {
		t.Fatal("aligned refs do not line up with the items")
	}
}
