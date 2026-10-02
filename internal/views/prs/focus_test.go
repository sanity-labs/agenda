package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func focusView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	// The preview pane is on: both panes are visible, so focus is asked
	// for rather than assumed. The float case is covered separately.
	v.Update(ui.PreviewShownMsg(true))
	return v
}

// Right focuses a pane, left gives the keys back: the same gesture for
// every pane, not just jobs.
func TestRightAndLeftMoveFocus(t *testing.T) {
	for _, c := range []struct {
		key  rune
		pane paneMode
	}{
		{'d', paneFiles}, {'c', paneComments},
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
		// Left returns focus from a scrolling pane. The file list collapses
		// first and releases on the next press, which its own test covers.
		v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if c.pane != paneFiles && v.PaneFocused() {
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
	v.Update(tea.KeyPressMsg{Code: 'c'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	if !v.Dismiss() {
		t.Fatal("esc did nothing with a focused pane open")
	}
	if v.PaneFocused() {
		t.Error("the first esc did not drop focus")
	}
	if v.pane != paneComments {
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

// A pane opened into a float takes the keys straight away: the list is
// behind it, so an explicit right would be a keystroke for nothing.
func TestFloatedPaneTakesFocusOnOpen(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false)) // hidden: panes float

	for _, key := range []rune{'d', 'c', 't'} {
		v.pane = paneBody
		v.Update(tea.KeyPressMsg{Code: key})
		if !v.PaneFocused() {
			t.Errorf("%q floated a pane without focusing it", key)
		}
	}
}

// With the preview on, both are visible, so focus waits to be asked for.
func TestPaneWaitsForFocusWhenThePreviewIsOn(t *testing.T) {
	v := focusView(t) // previewShown
	v.Update(tea.KeyPressMsg{Code: 'c'})
	if v.PaneFocused() {
		t.Error("a pane beside a visible list took focus on open")
	}
}

// Floated, esc goes to the description and then closes: there is no list
// beside the pane to hand the keys back to, so stepping through a focus
// state nothing can see would be a wasted press.
func TestEscInAFloatGoesToTheDescription(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false))

	for _, key := range []rune{'d', 'c', 't'} {
		v.pane = paneBody
		v.Update(tea.KeyPressMsg{Code: key})
		if v.pane == paneBody {
			t.Fatalf("%q did not open a pane", key)
		}
		if !v.Dismiss() {
			t.Errorf("esc did nothing in the floated %q pane", key)
		}
		if v.pane != paneBody {
			t.Errorf("esc from %q left pane=%v, want the description", key, v.pane)
		}
		if v.PaneFocused() {
			t.Errorf("esc from %q left the pane focused", key)
		}
	}
	// On the description there is nothing left, so the root model closes
	// the float.
	if v.Dismiss() {
		t.Error("esc claimed to act with only the description showing")
	}
}

// Submitting a review closes the pane you reviewed from, so the list must
// come back lit: a dimmed list with nothing focused says the arrows are
// somewhere they are not.
func TestReviewingUndimsTheList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false)) // floated

	v.Update(tea.KeyPressMsg{Code: 'd'})
	if !v.PaneFocused() {
		t.Fatal("setup: the floated pane is not focused")
	}

	v.Update(reviewDoneMsg{url: "u", what: "approved o/r#1", state: "APPROVED"})
	if v.PaneFocused() {
		t.Error("the list is still dimmed after reviewing")
	}
	if v.list.Blurred() {
		t.Error("the list is still blurred after reviewing")
	}
}

// With toggles: persist the pane deliberately stays open, so it keeps the
// keys and the list stays dimmed: that is the setting working, not focus
// stranded by a closed pane.
func TestPersistedToggleKeepsFocus(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.togglesPersist = true
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false))
	v.Update(tea.KeyPressMsg{Code: 'd'})

	v.Update(reviewDoneMsg{url: "u", what: "approved", state: "APPROVED"})
	if v.pane == paneBody {
		t.Fatal("toggles: persist closed the pane anyway")
	}
	if !v.PaneFocused() {
		t.Error("the pane stayed open but lost the keys")
	}
}

// Closing a pane by any route leaves nothing focused: focus belongs to a
// pane, so it cannot outlive one.
func TestFocusCannotOutliveItsPane(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !v.PaneFocused() {
		t.Fatal("setup: the pane is not focused")
	}
	// Whatever closes it, focus goes with it.
	v.pane = paneBody
	if v.PaneFocused() {
		t.Error("focus survived its pane closing")
	}
}

// A float takes the keys however it was opened. 'v' floats the
// description and must dim the list the same as 'c', 'd' or 't' do: the
// list is behind the window either way.
func TestFloatedDescriptionTakesFocus(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false))

	if !v.PaneFocused() {
		t.Error("a floated description does not have the keys")
	}
	if !v.PaneScrolls() {
		t.Error("a floated description should scroll, not hold a cursor")
	}
	v.ListView()
	if !v.list.Blurred() {
		t.Error("the list is not dimmed behind a floated description")
	}

	// Back beside a visible list, the description focuses nothing.
	v.Update(ui.PreviewShownMsg(true))
	if v.PaneFocused() {
		t.Error("the description took focus beside a visible list")
	}
}

// Floated, left and right do not move focus: there is no list beside the
// pane to hand the keys to, and esc is the way out.
func TestArrowsDoNotMoveFocusInAFloat(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false))
	v.Update(tea.KeyPressMsg{Code: 'c'})

	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !v.PaneFocused() {
		t.Error("left gave up focus in a float, leaving nothing holding the keys")
	}
}

// esc from any floated pane returns to the description, then closes.
func TestEscFromEachFloatedPane(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false))

	for _, k := range []rune{'c', 'd', 't'} {
		v.Update(tea.KeyPressMsg{Code: k})
		if v.pane == paneBody {
			t.Fatalf("%q did not open a pane", k)
		}
		if !v.Dismiss() || v.pane != paneBody {
			t.Errorf("esc from %q did not return to the description", k)
		}
	}
}
