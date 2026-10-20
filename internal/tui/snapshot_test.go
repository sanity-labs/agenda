package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func quitKey(m Model, k tea.KeyPressMsg) Model {
	got, _ := m.Update(k)
	return got.(Model)
}

// Only a q that quits saves; the q that closes a pane and ctrl+c do not.
func TestRememberOnQuit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	q := tea.KeyPressMsg{Code: 'q', Text: "q"}

	v := &filterView{paneView: paneView{open: 1}, query: "foo"}
	m := escModel(t, true, v)
	m.cfg.RememberOnQuit = true
	m = quitKey(m, q)
	fresh := &filterView{}
	if New(m.cfg, []View{fresh}).WithSnapshot(false); fresh.query != "" {
		t.Fatalf("q that closed a pane saved a snapshot: %q", fresh.query)
	}
	quitKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if New(m.cfg, []View{fresh}).WithSnapshot(false); fresh.query != "" {
		t.Fatalf("ctrl+c saved a snapshot: %q", fresh.query)
	}
	quitKey(m, q)
	if New(m.cfg, []View{fresh}).WithSnapshot(false); fresh.query != "foo" {
		t.Fatalf("restored query %q, want foo", fresh.query)
	}

	off := m.cfg
	off.RememberOnQuit = false
	other := &filterView{}
	if New(off, []View{other}).WithSnapshot(false); other.query != "" {
		t.Fatalf("restored with the setting off: %q", other.query)
	}
}
