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

// floatView opens a float the way the root model does for 'v': the view is
// told to focus, then the two preview messages land.
func floatView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false)) // startup, hide_preview
	return v
}

func pressV(v *View) {
	v.FocusPane(true)
	v.Update(ui.PreviewShownMsg(true))
	v.Update(ui.PreviewFloatingMsg(true))
}

// Floated, esc closes the whole float whatever level it is on: the arrows
// step a level at a time, esc is the way out. Reporting nothing left to do
// is how the view hands the close to the root model, which owns the float.
func TestEscClosesAFloatOutright(t *testing.T) {
	for _, key := range []rune{'d', 'c', 't'} {
		for _, viaV := range []bool{false, true} {
			v := floatView(t)
			if viaV {
				pressV(v)
			}
			v.Update(tea.KeyPressMsg{Code: key})
			if v.pane == paneBody {
				t.Fatalf("%q did not open a pane", key)
			}
			if v.Dismiss() {
				t.Errorf("esc from %q (via v: %v) stepped instead of closing", key, viaV)
			}
			if v.floatReveal || v.PaneFocused() || v.pane != paneBody {
				t.Errorf("esc from %q (via v: %v) left float=%v pane=%v focused=%v",
					key, viaV, v.floatReveal, v.pane, v.PaneFocused())
			}
		}
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
	// What 'v' actually sends: the detail is on screen, and the float flag
	// is what says it is over the list rather than beside it.
	v.Update(ui.PreviewFloatingMsg(true))
	v.Update(ui.PreviewShownMsg(true))

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
	v.Update(ui.PreviewFloatingMsg(false))
	v.Update(ui.PreviewShownMsg(true))
	if v.PaneFocused() {
		t.Error("the description took focus beside a visible list")
	}
}

func closes(t *testing.T, cmd tea.Cmd, what string) {
	t.Helper()
	if cmd == nil {
		t.Errorf("%s did nothing", what)
		return
	}
	if _, ok := cmd().(ui.ConcealPreviewMsg); !ok {
		t.Errorf("%s did not close the float", what)
	}
}

// A float opened straight from the list is level one, so an arrow that
// would leave the pane closes it: there is no list beside it to hand the
// keys to, and no description underneath to fall back to.
func TestArrowsCloseALevelOneFloat(t *testing.T) {
	open := map[string]func(v *View){
		"comments":    func(v *View) { v.Update(tea.KeyPressMsg{Code: 'c'}) },
		"description": pressV,
	}
	for name, openFloat := range open {
		for _, key := range []rune{tea.KeyLeft, tea.KeyRight} {
			v := floatView(t)
			openFloat(v)
			if !v.PaneFocused() {
				t.Fatalf("setup: the floated %s is not focused", name)
			}
			press := tea.KeyPressMsg{Code: key}
			closes(t, v.Update(press), press.String()+" in the floated "+name)
			if v.PaneFocused() || v.pane != paneBody {
				t.Errorf("%s closed the floated %s but left pane=%v focused=%v", press, name, v.pane, v.PaneFocused())
			}
		}
	}
}

// 'v' then a pane is two levels: left goes back to the description, which
// is still the float and still has the keys, and only the next left
// closes. Toggling the pane off with its own key is the same step.
func TestArrowsStepBackToTheDescription(t *testing.T) {
	for _, back := range []rune{tea.KeyLeft, 'c'} {
		v := floatView(t)
		pressV(v)
		v.Update(tea.KeyPressMsg{Code: 'c'})
		if v.pane != paneComments {
			t.Fatal("setup: 'c' did not open comments over the description")
		}

		press := tea.KeyPressMsg{Code: back}
		if cmd := v.Update(press); cmd != nil {
			if _, ok := cmd().(ui.ConcealPreviewMsg); ok {
				t.Errorf("%s closed the float instead of stepping back to the description", press)
			}
		}
		if v.pane != paneBody || !v.floatReveal {
			t.Errorf("%s left pane=%v float=%v, want the floated description", press, v.pane, v.floatReveal)
		}
		if !v.PaneFocused() {
			t.Errorf("the description lost the keys after %s", press)
		}

		closes(t, v.Update(tea.KeyPressMsg{Code: tea.KeyLeft}), "left on the description")
	}
}

// A pane toggled off with its own key at level one closes the float rather
// than leaving a description nobody asked for.
func TestTogglingALevelOnePaneOffClosesTheFloat(t *testing.T) {
	v := floatView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	closes(t, v.Update(tea.KeyPressMsg{Code: 'c'}), "'c' again")
	if v.floatReveal || v.PaneFocused() {
		t.Error("the view still thinks it is floating")
	}
}

// Startup with hide_preview is not a float: nothing is on screen over the
// list, so the list keeps the keys and stays lit. The preview being off and
// a float being open are different states, and only the second dims.
func TestHiddenPreviewAtStartupDoesNotDim(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(ui.PreviewShownMsg(false))

	if v.PaneFocused() {
		t.Error("the hidden preview took the keys with nothing on screen")
	}
	v.ListView()
	if v.list.Blurred() {
		t.Error("the list is dimmed at startup with nothing open")
	}

	// Opening a float then dims it, and closing it lights the list again.
	v.Update(tea.KeyPressMsg{Code: 'd'})
	if !v.PaneFocused() {
		t.Error("a float opened without taking the keys")
	}
	v.Update(ui.PreviewFloatingMsg(false))
	v.Update(ui.PreviewShownMsg(false))
	if v.PaneFocused() {
		t.Error("focus survived the float closing")
	}
}

// tea.Batch runs each command in its own goroutine, so the two messages a
// reveal sends land in either order. 'v' has no setPane to set the float
// flag up front, so it must come out focused whichever arrives first.
func TestFloatFocusSurvivesMessageOrder(t *testing.T) {
	for name, order := range map[string][]tea.Msg{
		"float then shown": {ui.PreviewFloatingMsg(true), ui.PreviewShownMsg(true)},
		"shown then float": {ui.PreviewShownMsg(true), ui.PreviewFloatingMsg(true)},
	} {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
		v.SetSize(90, 40, 20)
		p := pr{Number: 1, URL: "u", Title: "a title", State: "OPEN"}
		p.Repository.NameWithOwner = "o/r"
		v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
		v.Update(ui.PreviewShownMsg(false)) // startup, hide_preview
		v.FocusPane(true)                   // what the 'v' handler does first
		for _, msg := range order {
			v.Update(msg)
		}
		if !v.PaneFocused() {
			t.Errorf("%s: the floated description is not focused", name)
		}
	}
}

// Beside a visible list the same step applies one level down: left from a
// focused pane hands the keys to the list with the pane still open, and
// left again takes the pane back to the description. The configured pane
// is the floor and never closes.
func TestLeftStepsDownBesideTheList(t *testing.T) {
	for _, key := range []rune{'c', 'd', 't'} {
		v := focusView(t) // preview on
		v.Update(tea.KeyPressMsg{Code: key})
		if !v.PaneFocused() {
			v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		}
		if !v.PaneFocused() {
			t.Fatalf("setup: the %q pane is not focused", key)
		}
		want := v.pane

		v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if v.PaneFocused() {
			t.Errorf("left did not hand the keys back from the %q pane", key)
		}
		if v.pane != want {
			t.Errorf("left closed the %q pane along with releasing focus", key)
		}

		v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if v.pane != paneBody {
			t.Errorf("a second left did not take the %q pane back to the description", key)
		}
		if !v.previewShown {
			t.Error("the configured pane closed")
		}
	}
}
