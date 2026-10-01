package linear

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
)

func focusView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1", Title: "t"}}, source: v.defaultSource})
	return v
}

// The nav tree takes the left arrow, so the preview takes the right:
// symmetric, and the same gesture the PRs view uses.
func TestRightFocusesThePreview(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'}) // show comments: something to scroll
	if !v.showComments {
		t.Fatal("setup: comments are not showing")
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !v.PaneFocused() {
		t.Error("right did not focus the preview")
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if v.PaneFocused() {
		t.Error("left did not return focus to the list")
	}
}

// Nothing scrollable, nothing to focus: the arrows stay with the list.
func TestNoFocusWithoutComments(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if v.PaneFocused() {
		t.Error("the preview took focus with nothing in it")
	}
}

// The list dims for either focus target, since two lit cursors say
// nothing about which one the arrows move.
func TestListDimsForBothFocusTargets(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	lit := v.ListView()

	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if dim := v.ListView(); !strings.Contains(dim, "\x1b[2m") {
		t.Errorf("the list is not dimmed with the pane focused:\n%s", ansi.Strip(dim))
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if back := v.ListView(); strings.Contains(back, "\x1b[2m") != strings.Contains(lit, "\x1b[2m") {
		t.Error("the list did not undim when focus came back")
	}
}

// esc steps back: focus first, then the comments pane.
func TestEscStepsBackThroughFocus(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	if !v.Dismiss() || v.PaneFocused() {
		t.Error("the first esc did not drop focus")
	}
	if !v.showComments {
		t.Error("the first esc closed the pane as well")
	}
	if !v.Dismiss() || v.showComments {
		t.Error("the second esc did not close the comments pane")
	}
	if v.Dismiss() {
		t.Error("esc claimed to act with nothing left open")
	}
}
