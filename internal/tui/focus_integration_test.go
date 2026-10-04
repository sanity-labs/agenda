package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
	"github.com/sanity-labs/agenda/internal/views/prs"
)

// The real prs view inside the real root model. The other root tests use a
// stub view that does not implement paneFocuser, so nothing else here
// exercises the focus handshake: the key runs FocusPane immediately while
// the reveal's messages land afterwards, and a float arrives as
// PreviewShownMsg(true) just like a side pane does.
func focusIntegrationModel(t *testing.T) (Model, paneFocuser) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.HidePreview = true
	cfg.GitHub.DiffPane = true
	v := prs.New(cfg.GitHub, nil, nil, nil)
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	f, ok := m.views[0].(paneFocuser)
	if !ok {
		t.Fatal("the prs view does not satisfy paneFocuser")
	}
	return m, f
}

// Every key that opens a float focuses it, 'v' included.
func TestFloatKeysAutoFocusEndToEnd(t *testing.T) {
	for _, k := range []rune{'v', 'd', 'c', 't'} {
		m, f := focusIntegrationModel(t)
		if f.PaneFocused() {
			t.Fatalf("%q: focused before anything opened", k)
		}
		got, cmd := m.Update(tea.KeyPressMsg{Code: k})
		m = got.(Model)
		runCmds(t, &m, cmd, 0)

		if !m.floating() {
			t.Errorf("%q did not open a float", k)
		}
		if !f.PaneFocused() {
			t.Errorf("%q opened a float without focusing it", k)
		}
	}
}

// Nothing is on screen over the list at startup, so it keeps the keys.
func TestHiddenPreviewStartsUnfocusedEndToEnd(t *testing.T) {
	m, f := focusIntegrationModel(t)
	if m.floating() {
		t.Error("floating with nothing opened")
	}
	if f.PaneFocused() || m.paneFocused() {
		t.Error("the hidden preview took the keys at startup")
	}
}

// runCmds drains a command tree the way the runtime would, feeding every
// message back through Update.
func runCmds(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 6 {
		return
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			runCmds(t, m, c, depth+1)
		}
		return
	}
	if msg == nil {
		return
	}
	got, next := m.Update(msg)
	*m = got.(Model)
	runCmds(t, m, next, depth+1)
}

// The same keys with every batch delivered back to front: tea.Batch runs
// its commands concurrently, so the real order is whichever goroutine
// wins, and the result must not depend on it.
func TestFloatKeysAutoFocusWhicheverOrder(t *testing.T) {
	for _, k := range []rune{'v', 'd', 'c', 't'} {
		m, f := focusIntegrationModel(t)
		got, cmd := m.Update(tea.KeyPressMsg{Code: k})
		m = got.(Model)
		runCmdsReversed(t, &m, cmd, 0)
		if !m.floating() {
			t.Errorf("%q did not open a float", k)
		}
		if !f.PaneFocused() {
			t.Errorf("%q opened a float without focusing it (messages reversed)", k)
		}
	}
}

func runCmdsReversed(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 6 {
		return
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for i := len(b) - 1; i >= 0; i-- {
			runCmdsReversed(t, m, b[i], depth+1)
		}
		return
	}
	if msg == nil {
		return
	}
	got, next := m.Update(msg)
	*m = got.(Model)
	runCmdsReversed(t, m, next, depth+1)
}

// Preview messages reach every view, so a view that is not on screen hears
// a float open. Switching tabs closes the float; the close has to reach the
// views too, or the tab you land on is dimmed for a window that is gone.
func TestTabSwitchUndimsTheNextView(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.HidePreview = true
	cfg.GitHub.DiffPane = true
	pv := prs.New(cfg.GitHub, nil, nil, nil)
	m := New(cfg, []View{&fatView{title: "Linear"}, pv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()

	got, cmd := m.Update(ui.RevealPreviewMsg{}) // 'v' on the Linear tab
	m = got.(Model)
	runCmds(t, &m, cmd, 0)
	if !m.floating() {
		t.Fatal("setup: the preview did not float")
	}

	got, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = got.(Model)
	runCmds(t, &m, cmd, 0)
	if m.current != 1 {
		t.Fatalf("setup: tab did not switch to the PRs view (current=%d)", m.current)
	}
	if m.floating() {
		t.Error("the float survived the tab switch")
	}
	if pv.PaneFocused() {
		t.Error("the PRs list is still dimmed after switching to it")
	}
}

// jumpPaneView is a tall, scrolling pane that always has the keys: what a
// floated description looks like to the root model.
type jumpPaneView struct{ tallView }

func (v *jumpPaneView) PaneFocused() bool   { return true }
func (v *jumpPaneView) FocusPane(bool) bool { return false }
func (v *jumpPaneView) PaneScrolls() bool   { return true }

// Jump keys move whatever has the keys. In a focused float they scroll the
// pane by list_jump lines; before this the root let shift+arrows through to
// the view, where the list moved, the selection changed and the float
// closed from under you.
func TestJumpKeysScrollAFocusedFloat(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	v := &jumpPaneView{tallView{fatView: fatView{title: "PRs"}, lines: 80}}
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	if !m.floating() || !m.paneFocused() {
		t.Fatalf("setup: float=%v paneFocused=%v", m.floating(), m.paneFocused())
	}

	got, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	m = got.(Model)
	if m.previewScroll != cfg.ListJumpSize() {
		t.Errorf("shift+down scrolled the float by %d, want %d", m.previewScroll, cfg.ListJumpSize())
	}
	got, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = got.(Model)
	if m.previewScroll != 2*cfg.ListJumpSize() {
		t.Errorf("pgdn scrolled the float to %d, want %d", m.previewScroll, 2*cfg.ListJumpSize())
	}
	got, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	m = got.(Model)
	if m.previewScroll != cfg.ListJumpSize() || !m.floating() {
		t.Errorf("shift+up: scroll=%d float=%v, want %d and still floating", m.previewScroll, m.floating(), cfg.ListJumpSize())
	}
}
