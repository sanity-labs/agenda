package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The jump keys move a fixed number of rows, five unless configured, and
// clamp at the ends; shift+arrows and page keys are the same gesture.
func TestJumpKeysMoveByTheConfiguredStep(t *testing.T) {
	l := NewList[strItem]()
	items := make([]strItem, 12)
	for i := range items {
		items[i] = strItem(string(rune('a' + i)))
	}
	l.SetItems(items)
	l.SetSize(40, 20)

	l.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if l.cursor != 5 {
		t.Errorf("pgdn moved to %d, want 5 by default", l.cursor)
	}
	l.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if l.cursor != 10 {
		t.Errorf("shift+down moved to %d, want 10", l.cursor)
	}
	l.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if l.cursor != 11 {
		t.Errorf("pgdn past the end landed on %d, want the last row 11", l.cursor)
	}
	l.SetJump(3)
	l.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if l.cursor != 8 {
		t.Errorf("shift+up with jump 3 moved to %d, want 8", l.cursor)
	}
	l.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	l.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	l.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if l.cursor != 0 {
		t.Errorf("pgup past the top landed on %d, want 0", l.cursor)
	}
}
