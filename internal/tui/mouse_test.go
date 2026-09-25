package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// clickStub is a stubView whose list has one single-line row per entry and
// records the mouse calls it receives.
type clickStub struct {
	stubView
	rows      []string
	sel       int
	clicks    [][2]int
	scrolled  int
	activated int
	input     bool
}

func (c *clickStub) ClickList(x, y int) (bool, tea.Cmd) {
	c.clicks = append(c.clicks, [2]int{x, y})
	if y < 0 || y >= len(c.rows) {
		return false, nil
	}
	c.sel = y
	return true, nil
}
func (c *clickStub) ScrollList(n int) tea.Cmd { c.scrolled += n; return nil }
func (c *clickStub) Activate() tea.Cmd        { c.activated++; return nil }
func (c *clickStub) PreviewKey() string       { return c.rows[c.sel] }
func (c *clickStub) InputActive() bool        { return c.input }
func (c *clickStub) PreviewView() string      { return strings.Repeat("line\n", 100) }

func newClickModel(views ...View) Model {
	m := New(config.Default(), views)
	m.width, m.height, m.ready = 120, 40, true
	return m
}

func leftClick(m Model, x, y int) Model {
	got, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return got.(Model)
}

func TestClickTabSwitchesView(t *testing.T) {
	m := newClickModel(&stubView{"PRs"}, &stubView{"Sessions"}, &stubView{"Linear"})

	// The first column of the second label.
	x := lipgloss.Width(m.tabLabels()[0])
	if m = leftClick(m, x, 0); m.current != 1 {
		t.Fatalf("click at x=%d on the tab bar: current = %d, want 1", x, m.current)
	}
	if m = leftClick(m, m.width-1, 0); m.current != 1 {
		t.Errorf("click past the last tab switched to view %d", m.current)
	}
}

func TestClickListGetsPaneCoordinates(t *testing.T) {
	v := &clickStub{stubView: stubView{"PRs"}, rows: []string{"a", "b", "c", "d"}}
	m := newClickModel(v)

	m = leftClick(m, 5, tabBarHeight+3)
	if len(v.clicks) != 1 || v.clicks[0] != [2]int{5, 3} {
		t.Fatalf("clicks = %v, want one at pane (5,3)", v.clicks)
	}
	if v.sel != 3 {
		t.Errorf("sel = %d, want 3", v.sel)
	}

	listW, _, _ := m.dims()
	leftClick(m, listW, tabBarHeight+1)    // preview column
	leftClick(m, 5, m.height-footerHeight) // footer
	if len(v.clicks) != 1 {
		t.Errorf("clicks outside the list column reached the view: %v", v.clicks[1:])
	}
}

func TestDoubleClickActivatesSameRowOnly(t *testing.T) {
	v := &clickStub{stubView: stubView{"PRs"}, rows: []string{"a", "b", "c"}}
	m := newClickModel(v)
	row := func(i int) int { return tabBarHeight + i }

	m = leftClick(leftClick(m, 1, row(0)), 1, row(1))
	if v.activated != 0 {
		t.Fatal("clicks on two different rows counted as a double-click")
	}
	m = leftClick(m, 1, row(1))
	if v.activated != 1 {
		t.Fatalf("second click on the same row: activated = %d, want 1", v.activated)
	}
	m = leftClick(m, 1, row(1))
	if v.activated != 1 {
		t.Error("a third click activated again; it should start a new pair")
	}

	m.lastClick.at = time.Now().Add(-time.Second) // the previous click was long ago
	leftClick(m, 1, row(1))
	if v.activated != 1 {
		t.Error("a slow second click counted as a double-click")
	}
}

func TestClickIgnoredWhileViewTakesInput(t *testing.T) {
	v := &clickStub{stubView: stubView{"PRs"}, rows: []string{"a", "b"}, input: true}
	m := newClickModel(v, &stubView{"Linear"})

	m = leftClick(m, 1, tabBarHeight+1)
	m = leftClick(m, lipgloss.Width(m.tabLabels()[0]), 0)
	if len(v.clicks) != 0 || m.current != 0 {
		t.Errorf("clicks acted during text input: clicks=%v current=%d", v.clicks, m.current)
	}
}

func TestClickClosesHelpWithoutActing(t *testing.T) {
	v := &clickStub{stubView: stubView{"PRs"}, rows: []string{"a", "b"}}
	m := newClickModel(v)
	m.helpOpen = true

	if m = leftClick(m, 1, tabBarHeight+1); m.helpOpen {
		t.Error("help overlay still open after a click")
	}
	if len(v.clicks) != 0 {
		t.Error("the click that closed help also reached the list")
	}
}

func TestClickOutsidePickerClosesIt(t *testing.T) {
	m := newClickModel(&stubView{"PRs"})
	p := ui.NewPicker("Follow reference", []ui.PickerItem{{Label: "SRE-1"}})
	m.picker, m.pickerRefs = &p, []ui.Ref{{}}

	x, y := m.centerOf(m.picker.View())
	if m = leftClick(m, x+1, y+1); m.picker == nil {
		t.Fatal("click inside the picker closed it")
	}
	if m = leftClick(m, 0, m.height-1); m.picker != nil {
		t.Error("click outside the picker left it open")
	}
}

func TestWheelScrollsOneStepUnderPointer(t *testing.T) {
	v := &clickStub{stubView: stubView{"PRs"}, rows: []string{"a"}}
	m := newClickModel(v)
	listW, _, _ := m.dims()
	wheel := func(m Model, x int, b tea.MouseButton) Model {
		got, _ := m.Update(tea.MouseWheelMsg{X: x, Y: tabBarHeight + 1, Button: b})
		return got.(Model)
	}

	m = wheel(m, 1, tea.MouseWheelDown)
	if v.scrolled != 1 {
		t.Errorf("wheel over the list scrolled %d rows, want 1", v.scrolled)
	}
	m = wheel(m, listW+2, tea.MouseWheelDown)
	if m.previewScroll != 1 {
		t.Errorf("wheel over the preview scrolled %d lines, want 1", m.previewScroll)
	}

	m.helpOpen = true
	m = wheel(m, 1, tea.MouseWheelDown)
	m = wheel(m, listW+2, tea.MouseWheelDown)
	if v.scrolled != 1 || m.previewScroll != 1 {
		t.Errorf("wheel reached the panes under the help overlay (list %d, preview %d)", v.scrolled, m.previewScroll)
	}
}

func TestWheelNotchScrollsOnce(t *testing.T) {
	v := &clickStub{stubView: stubView{"PRs"}, rows: []string{"a"}}
	m := newClickModel(v)
	// As the event loop does: Update, then draw a frame.
	step := func(m Model, b tea.MouseButton) Model {
		got, _ := m.Update(tea.MouseWheelMsg{X: 1, Y: tabBarHeight + 1, Button: b})
		m = got.(Model)
		m.View()
		return m
	}

	// A notch: three events, all queued before the first frame finished.
	for range 3 {
		m = step(m, tea.MouseWheelDown)
	}
	if v.scrolled != 1 {
		t.Fatalf("one notch (3 queued events) scrolled %d rows, want 1", v.scrolled)
	}

	// A trackpad swipe: each event finds the loop idle since its last frame.
	for range 3 {
		m.wheelSt.drawnAt = time.Now().Add(-10 * time.Millisecond)
		m = step(m, tea.MouseWheelDown)
	}
	if v.scrolled != 4 {
		t.Fatalf("three spaced events scrolled %d rows in total, want 4", v.scrolled)
	}

	// Reversing is a new scroll even straight after a frame.
	if m = step(m, tea.MouseWheelUp); v.scrolled != 3 {
		t.Fatalf("reversal: scrolled = %d, want 3", v.scrolled)
	}

	// A frame drawn for something else (a spinner tick) just before a new
	// notch doesn't swallow it.
	m.wheelSt.at = time.Now().Add(-time.Second)
	m.View()
	if step(m, tea.MouseWheelUp); v.scrolled != 2 {
		t.Errorf("first event of a later notch was dropped: scrolled = %d, want 2", v.scrolled)
	}
}

func TestWheelNotchScrollsPreviewPerEvent(t *testing.T) {
	m := newClickModel(&clickStub{stubView: stubView{"PRs"}, rows: []string{"a"}})
	listW, _, _ := m.dims()
	for range 3 { // one notch, queued as in TestWheelNotchScrollsOnce
		got, _ := m.Update(tea.MouseWheelMsg{X: listW + 2, Y: tabBarHeight + 1, Button: tea.MouseWheelDown})
		m = got.(Model)
		m.View()
	}
	if m.previewScroll != 3 {
		t.Errorf("a three-event notch over the preview scrolled %d lines, want 3", m.previewScroll)
	}
}
