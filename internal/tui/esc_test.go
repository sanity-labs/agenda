package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// paneView is a view with a closable pane, to exercise esc's ordering.
type paneView struct {
	fatView
	open int // how many layers are open
}

func (p *paneView) Dismiss() bool {
	if p.open > 0 {
		p.open--
		return true
	}
	return false
}

func escModel(t *testing.T, hidePreview bool, v View) Model {
	t.Helper()
	cfg := config.Default()
	cfg.HidePreview = hidePreview
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 120, 40, true
	m.layout()
	return m
}

func esc(m Model) Model {
	got, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	return got.(Model)
}

// esc must never quit: that is 'q' alone. A stray esc on a bare list is a
// no-op, not an exit.
func TestEscNeverQuits(t *testing.T) {
	for _, hidden := range []bool{true, false} {
		m := escModel(t, hidden, &fatView{title: "PRs"})
		got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Errorf("hide_preview=%v: esc quit the app", hidden)
			}
		}
		if len(got.(Model).views) == 0 {
			t.Errorf("hide_preview=%v: esc tore the views down", hidden)
		}
	}
}

// With the preview off, esc closes a floating detail.
func TestEscClosesTheFloat(t *testing.T) {
	m := escModel(t, true, &fatView{title: "PRs"})
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	if !m.floating() {
		t.Fatal("setup: not floating")
	}
	if m = esc(m); m.floating() {
		t.Error("esc left the float open")
	}
}

// With the preview on, esc closes the view's pane but not the pane itself:
// the preview is the configured state, not something esc opened.
func TestEscKeepsTheConfiguredPreview(t *testing.T) {
	v := &paneView{fatView: fatView{title: "PRs"}, open: 1}
	m := escModel(t, false, v)
	if m.previewHidden {
		t.Fatal("setup: the preview should be showing")
	}
	m = esc(m)
	if v.open != 0 {
		t.Error("esc did not close the view's pane")
	}
	if m.previewHidden {
		t.Error("esc hid the preview pane, which is the configured state")
	}
}

// Layers close one at a time, innermost first, rather than collapsing at
// once: esc walks back out the way you came in.
func TestEscUnwindsOneLayerAtATime(t *testing.T) {
	v := &paneView{fatView: fatView{title: "PRs"}, open: 3}
	m := escModel(t, true, v)
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)

	for want := 2; want >= 0; want-- {
		m = esc(m)
		if v.open != want {
			t.Fatalf("esc left %d layers open, want %d", v.open, want)
		}
		if want > 0 && !m.floating() {
			t.Error("esc closed the float while panes were still open")
		}
	}
	// The last pane is gone, so now esc closes the float itself.
	if !m.floating() {
		t.Fatal("the float closed too early")
	}
	if m = esc(m); m.floating() {
		t.Error("esc did not close the float once the panes were gone")
	}
}

// Zoom is the outermost thing esc can undo.
func TestEscUnzooms(t *testing.T) {
	m := escModel(t, false, &fatView{title: "PRs"})
	m.zoomed = true
	if m = esc(m); m.zoomed {
		t.Error("esc did not leave zoom")
	}
}

// The focused view gets esc before the root model does, so a pane can
// unwind its own state first. Intercepting it here meant the jobs pane
// never saw esc at all: it closed outright instead of dropping focus.
func TestEscReachesTheViewFirst(t *testing.T) {
	v := &paneView{fatView: fatView{title: "PRs"}, open: 2}
	m := escModel(t, false, v)

	m = esc(m)
	if v.open != 1 {
		t.Fatalf("the view has %d layers open, want 1: esc did not reach it", v.open)
	}
	// And the root model did not also act on the same press.
	if m.previewHidden {
		t.Error("esc both unwound the view and hid the preview")
	}
}

// filterView is a list with a filter query and one closable layer.
type filterView struct {
	paneView
	query string
}

func (f *filterView) Fields() []string                       { return []string{"title"} }
func (f *filterView) FilterState() (string, []string, bool)  { return f.query, []string{"title"}, false }
func (f *filterView) SetFilter(q string, _ []string, _ bool) { f.query = q }

// With nothing open over the list, esc clears the filter; a pane still
// open closes first and the query survives that press.
func TestEscClearsTheFilterLast(t *testing.T) {
	v := &filterView{paneView: paneView{open: 1}, query: "foo"}
	m := escModel(t, true, v)
	m = esc(m)
	if v.open != 0 || v.query != "foo" {
		t.Fatalf("after one esc: open=%d query=%q, want the pane closed and the query kept", v.open, v.query)
	}
	m = esc(m)
	if v.query != "" {
		t.Fatalf("after the second esc: query=%q, want it cleared", v.query)
	}
	esc(m) // nothing left: still no quit, no panic
}
