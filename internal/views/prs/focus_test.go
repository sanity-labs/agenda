package prs

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
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	return v
}

// Right focuses a pane, left gives the keys back: the same gesture for
// every pane, not just jobs.
func TestRightAndLeftMoveFocus(t *testing.T) {
	for _, c := range []struct {
		key  rune
		pane paneMode
	}{
		{'d', paneDiff}, {'c', paneComments},
	} {
		v := focusView(t)
		v.Update(tea.KeyPressMsg{Code: c.key})
		if v.pane != c.pane {
			t.Fatalf("%q did not open its pane (pane=%v)", c.key, v.pane)
		}
		if v.PaneFocused() {
			t.Errorf("%q opened the pane already focused", c.key)
		}

		v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if !v.PaneFocused() {
			t.Errorf("right did not focus the %q pane", c.key)
		}
		v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if v.PaneFocused() {
			t.Errorf("left did not return focus from the %q pane", c.key)
		}
	}
}

// The list dims while a pane has the keys, so only one cursor is lit.
func TestFocusDimsTheList(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'd'})

	lit := strings.Split(v.ListView(), "\n")[1]
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	dim := strings.Split(v.ListView(), "\n")[1]

	if !strings.Contains(lit, "▌") {
		t.Errorf("the unfocused list has no selection bar: %q", ansi.Strip(lit))
	}
	if strings.Contains(dim, "▌") {
		t.Errorf("the blurred list still shows its bar: %q", ansi.Strip(dim))
	}
	if !strings.Contains(dim, "\x1b[2m") {
		t.Errorf("the blurred list is not dimmed: %q", dim)
	}
}

// Changing panes drops focus: a new pane starts beside a lit list, so the
// arrows do not silently belong to something that just appeared.
func TestSwitchingPanesDropsFocus(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'd'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !v.PaneFocused() {
		t.Fatal("setup: the diff pane is not focused")
	}
	v.Update(tea.KeyPressMsg{Code: 'c'})
	if v.PaneFocused() {
		t.Error("switching to the comments pane kept focus")
	}
}

// esc steps back: focus first, then the pane.
func TestEscStepsBackThroughFocus(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'd'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	if !v.Dismiss() {
		t.Fatal("esc did nothing with a focused pane open")
	}
	if v.PaneFocused() {
		t.Error("the first esc did not drop focus")
	}
	if v.pane != paneDiff {
		t.Error("the first esc closed the pane as well as dropping focus")
	}
	if !v.Dismiss() {
		t.Fatal("the second esc did nothing")
	}
	if v.pane != paneBody {
		t.Error("the second esc did not close the pane")
	}
	if v.Dismiss() {
		t.Error("esc claimed to act with nothing left open")
	}
}

// The description pane has nothing to focus: it just scrolls.
func TestBodyPaneTakesNoFocus(t *testing.T) {
	v := focusView(t)
	if v.FocusPane(true) {
		t.Error("the body pane accepted focus")
	}
	if v.PaneFocused() {
		t.Error("the body pane reports focus")
	}
}
