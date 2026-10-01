package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
)

// settingsAt opens the overlay and returns the model plus the box's
// top-left corner, so a test can click a known row.
func settingsAt(t *testing.T) (Model, int, int) {
	t.Helper()
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 120, 50, true
	m.layout()
	m.settings = newConfigOverlay()
	w, _ := m.settings.Size(m.cfg)
	return m, max(0, (m.width-w)/2), m.settings.TopY(m.cfg, m.height)
}

// RowAt has to agree with what View actually prints, or clicks land on the
// wrong setting. Checked against the render rather than the arithmetic
// that produced it.
func TestRowAtMatchesTheRender(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	for i := range settingsTabs {
		o.tab = i
		o.cursor = o.firstSetting()
		lines := strings.Split(o.View(cfg), "\n")
		hit := 0
		for y, line := range lines {
			row := o.RowAt(0, 0, 0, y)
			if row < 0 {
				continue
			}
			hit++
			if text := ansi.Strip(line); !strings.Contains(text, o.rows[row].label) {
				t.Errorf("tab %q line %d: RowAt says %q, the line reads %q",
					settingsTabs[i].name, y, o.rows[row].label, strings.TrimSpace(text))
			}
		}
		// Every setting in the tab must be clickable, or some are keyboard-only.
		want := 0
		for _, idx := range o.visible() {
			if o.rows[idx].kind != kindHeader {
				want++
			}
		}
		if hit != want {
			t.Errorf("tab %q: %d rows are clickable, %d are shown",
				settingsTabs[i].name, hit, want)
		}
	}
}

// A click on a bool toggles it on the first click: the row under the
// pointer is the one you meant.
func TestClickTogglesABool(t *testing.T) {
	m, bx, by := settingsAt(t)
	// Find a bool row and the line it renders on.
	lines := strings.Split(m.settings.View(m.cfg), "\n")
	target, line := -1, -1
	for y := range lines {
		if row := m.settings.RowAt(bx, by, bx, by+y); row >= 0 &&
			m.settings.rows[row].kind == kindBool {
			target, line = row, y
			break
		}
	}
	if target < 0 {
		t.Fatal("no bool row on the first tab")
	}
	before := m.settings.rows[target].get(m.cfg)

	got, cmd := m.clickSettings(bx+4, by+line)
	m = got.(Model)
	if cmd == nil {
		t.Fatal("clicking a bool produced no change")
	}
	if m.settings.cursor != target {
		t.Errorf("cursor is %d after the click, want the clicked row %d",
			m.settings.cursor, target)
	}
	if after := m.settings.rows[target].get(m.cfg); after == before {
		t.Errorf("the bool is still %q after a click", after)
	}
}

// A click on the tab bar switches tabs.
func TestClickSwitchesTabs(t *testing.T) {
	m, bx, by := settingsAt(t)
	// The second tab's label starts after the first plus two spaces.
	col := bx + 3 + lipgloss.Width(settingsTabs[0].name) + 2
	got, _ := m.clickSettings(col, by+3)
	m = got.(Model)
	if m.settings == nil {
		t.Fatal("clicking a tab closed the overlay")
	}
	if m.settings.tab != 1 {
		t.Errorf("tab = %d after clicking the second, want 1", m.settings.tab)
	}
	if k := m.settings.rows[m.settings.cursor].kind; k == kindHeader {
		t.Error("the cursor landed on a header after switching tabs")
	}
}

// A click outside closes the overlay, like the other modals.
func TestClickOutsideClosesSettings(t *testing.T) {
	m, _, _ := settingsAt(t)
	got, _ := m.clickSettings(0, 0)
	if got.(Model).settings != nil {
		t.Error("a click outside the box left the overlay open")
	}
}

// A click on the chrome or a header selects nothing and changes nothing.
func TestClickOnChromeDoesNothing(t *testing.T) {
	m, bx, by := settingsAt(t)
	before := m.settings.cursor
	for _, y := range []int{0, 1, 2} { // border, title, blank
		got, cmd := m.clickSettings(bx+4, by+y)
		m = got.(Model)
		if m.settings == nil {
			t.Fatalf("a click on row %d closed the overlay", y)
		}
		if cmd != nil {
			t.Errorf("a click on row %d committed a change", y)
		}
		if m.settings.cursor != before {
			t.Errorf("a click on row %d moved the cursor", y)
		}
	}
}
