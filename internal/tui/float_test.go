package tui

import (
	tea "charm.land/bubbletea/v2"

	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func floatModel(t *testing.T) Model {
	t.Helper()
	cfg := config.Default()
	cfg.HidePreview = true
	m := New(cfg, []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	return m
}

// With the pane off, a transient reveal floats over the list instead of
// splitting it: the list keeps the full width underneath.
func TestTransientRevealFloats(t *testing.T) {
	m := floatModel(t)
	if m.floating() {
		t.Fatal("floating before anything revealed the preview")
	}

	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	if !m.floating() {
		t.Error("a transient reveal over a hidden pane did not float")
	}
	if listW, _, _ := m.dims(); listW != m.width {
		t.Errorf("list width = %d while floating, want the full %d", listW, m.width)
	}

	got, _ = m.Update(ui.ConcealPreviewMsg{})
	m = got.(Model)
	if m.floating() {
		t.Error("the float outlived the reveal that created it")
	}
}

// With hide_preview on, 'v' is the float: it is the only way to see the
// detail, and splitting the pane would defeat the setting. Pressing it
// again closes the float.
func TestToggleKeyFloatsWhenPreviewHidden(t *testing.T) {
	m := floatModel(t) // hide_preview: true
	press := tea.KeyPressMsg{Code: 'v'}

	got, _ := m.Update(press)
	m = got.(Model)
	if !m.floating() {
		t.Fatal("'v' with hide_preview on opened a pane, want the float")
	}
	if listW, _, _ := m.dims(); listW != m.width {
		t.Errorf("list width = %d while floating, want the full %d", listW, m.width)
	}

	got, _ = m.Update(press)
	m = got.(Model)
	if m.floating() {
		t.Error("'v' again left the float open, want it closed")
	}
}

// With the pane configured on, 'v' keeps its original meaning: hide and
// show the side pane, no float involved.
func TestToggleKeyIsAPaneWhenPreviewShown(t *testing.T) {
	cfg := config.Default() // hide_preview defaults to false
	m := New(cfg, []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()

	got, _ := m.Update(tea.KeyPressMsg{Code: 'v'})
	m = got.(Model)
	if m.floating() {
		t.Error("'v' floated with the pane configured on, want the pane hidden")
	}
	if !m.previewHidden {
		t.Error("'v' did not hide the pane")
	}

	got, _ = m.Update(tea.KeyPressMsg{Code: 'v'})
	m = got.(Model)
	if m.previewHidden || m.floating() {
		t.Error("'v' again did not bring the pane back")
	}
}

// Zoom is an explicit full-screen request and outranks the float.
func TestZoomOutranksFloat(t *testing.T) {
	m := floatModel(t)
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	m.zoomed = true
	if m.floating() {
		t.Error("floated while zoomed; zoom should win")
	}
}

// The views are told whether the detail is on screen, since that decides
// what counts as reading a row.
func TestPreviewVisibilityIsBroadcast(t *testing.T) {
	m := floatModel(t)
	cmd := m.setPreview(false, true)
	if cmd == nil {
		t.Fatal("showing the preview broadcast nothing")
	}
	// A reveal changes both facts at once: shown, and shown as a float.
	var shown, floating bool
	for _, c := range cmd().(tea.BatchMsg) {
		switch msg := c().(type) {
		case ui.PreviewShownMsg:
			shown = bool(msg)
		case ui.PreviewFloatingMsg:
			floating = bool(msg)
		}
	}
	if !shown {
		t.Error("the reveal did not broadcast PreviewShownMsg(true)")
	}
	if !floating {
		t.Error("a transient reveal did not broadcast PreviewFloatingMsg(true)")
	}
	// No change, no message.
	if cmd := m.setPreview(false, true); cmd != nil {
		t.Errorf("re-showing an already-shown preview broadcast %#v", cmd())
	}
}
