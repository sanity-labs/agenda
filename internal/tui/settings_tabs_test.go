package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

// Every section in the table must belong to a tab, or its settings are
// unreachable: the rows still exist, the overlay just never shows them.
func TestEverySectionIsInATab(t *testing.T) {
	inTabs := map[string]bool{}
	for _, tb := range settingsTabs {
		for _, sec := range tb.sections {
			if inTabs[sec] {
				t.Errorf("section %q is in two tabs", sec)
			}
			inTabs[sec] = true
		}
	}
	for _, s := range settingsTable() {
		if s.kind == kindHeader && !inTabs[s.label] {
			t.Errorf("section %q is in no tab, so its settings are unreachable", s.label)
		}
	}
	// And no tab names a section that does not exist.
	have := map[string]bool{}
	for _, s := range settingsTable() {
		if s.kind == kindHeader {
			have[s.label] = true
		}
	}
	for sec := range inTabs {
		if !have[sec] {
			t.Errorf("a tab names %q, which is not a section in the table", sec)
		}
	}
}

// Every setting is reachable by tabbing and moving, which is the property
// the tabs must not break.
func TestEverySettingIsReachable(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()

	seen := map[int]bool{}
	for tab := 0; tab < len(settingsTabs); tab++ {
		for i := 0; i < len(o.rows)+1; i++ {
			seen[o.cursor] = true
			before := o.cursor
			o.Update(press('j'), cfg)
			if o.cursor == before {
				break
			}
		}
		o.Update(special(tea.KeyTab), cfg)
	}

	for i, s := range o.rows {
		if s.kind != kindHeader && !seen[i] {
			t.Errorf("row %d (%s, %s) cannot be reached", i, s.label, s.path)
		}
	}
}

// The cursor never leaves the active tab: moving past the last row stays
// put rather than wandering into a section the tab bar says is elsewhere.
func TestCursorStaysInsideTheTab(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	for tab := range settingsTabs {
		o.tab = tab
		o.cursor = o.firstSetting()
		in := map[int]bool{}
		for _, idx := range o.visible() {
			in[idx] = true
		}
		for _, key := range []rune{'j', 'k'} {
			for i := 0; i < len(o.rows)*2; i++ {
				o.Update(press(key), cfg)
				if !in[o.cursor] {
					t.Fatalf("tab %q: cursor escaped to row %d (%s), which the"+
						" tab does not show", settingsTabs[tab].name,
						o.cursor, o.rows[o.cursor].label)
				}
			}
		}
	}
}

// Tab wraps both ways, and lands on a setting rather than a header.
func TestTabWrapsAndLandsOnASetting(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	for i := 0; i < len(settingsTabs); i++ {
		if k := o.rows[o.cursor].kind; k == kindHeader {
			t.Errorf("tab %q opened with the cursor on a header",
				settingsTabs[o.tab].name)
		}
		o.Update(special(tea.KeyTab), cfg)
	}
	if o.tab != 0 {
		t.Errorf("tabbing through every tab ended on %d, want back at 0", o.tab)
	}
	o.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, cfg)
	if o.tab != len(settingsTabs)-1 {
		t.Errorf("shift+tab from the first tab went to %d, want the last", o.tab)
	}
}

// The view shows only the active tab's rows, so switching tabs actually
// changes what is on screen.
func TestViewShowsOnlyTheActiveTab(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()

	for settingsTabs[o.tab].name != "appearance" {
		o.Update(special(tea.KeyTab), cfg)
	}
	appearance := o.View(cfg)
	if !strings.Contains(appearance, "palette") {
		t.Error("the appearance tab does not show the palette row")
	}
	if strings.Contains(appearance, "page size") {
		t.Error("the appearance tab is showing a PRs row")
	}

	// Switch to the PRs tab.
	for settingsTabs[o.tab].name != "prs" {
		o.Update(special(tea.KeyTab), cfg)
	}
	prs := o.View(cfg)
	if !strings.Contains(prs, "page size") {
		t.Error("the prs tab does not show the page size row")
	}
	if strings.Contains(prs, "palette") {
		t.Error("the prs tab is showing an appearance row")
	}
}

// Editing state does not survive a tab switch: a half-typed value applying
// to whatever row the new tab lands on would be its own bug.
func TestTabSwitchCancelsEditing(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	if !seekKind(o, cfg, kindText) {
		t.Fatal("no text row reachable")
	}
	o.Update(special(tea.KeyEnter), cfg)
	o.Update(press('9'), cfg)
	if !o.editing {
		t.Fatal("setup: not editing")
	}
	o.Update(special(tea.KeyTab), cfg)
	if o.editing || o.buf != "" {
		t.Errorf("editing survived a tab switch: editing=%v buf=%q", o.editing, o.buf)
	}
}

// The panel must not resize as you move between tabs: a box that changes
// size under the cursor reads as the whole panel jumping.
func TestOverlayWidthIsConstantAcrossTabs(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	want := 0
	for i := range settingsTabs {
		o.tab = i
		o.cursor = o.firstSetting()
		v := o.View(cfg)
		w := lipgloss.Width(v)
		if i == 0 {
			want = w
			continue
		}
		if w != want {
			t.Errorf("tab %q renders %d wide, the first renders %d",
				settingsTabs[i].name, w, want)
		}
	}
	// And nothing inside wraps: a wrapped line means the content is wider
	// than the box, which the rule and the hint have both done.
	for i := range settingsTabs {
		o.tab = i
		o.cursor = o.firstSetting()
		for _, line := range strings.Split(o.View(cfg), "\n") {
			if lipgloss.Width(line) != want {
				t.Errorf("tab %q has a line %d wide in a %d box: %q",
					settingsTabs[i].name, lipgloss.Width(line), want, line)
			}
		}
	}
}

// Sections render in the order the tab lists them, not the table's.
func TestSectionsFollowTheTabOrder(t *testing.T) {
	o := newConfigOverlay()
	for i, tb := range settingsTabs {
		o.tab = i
		o.cursor = o.firstSetting()
		var seen []string
		for _, idx := range o.visible() {
			if o.rows[idx].kind == kindHeader {
				seen = append(seen, o.rows[idx].label)
			}
		}
		if len(seen) != len(tb.sections) {
			t.Errorf("tab %q shows %v, wants %v", tb.name, seen, tb.sections)
			continue
		}
		for j := range seen {
			if seen[j] != tb.sections[j] {
				t.Errorf("tab %q section %d is %q, want %q",
					tb.name, j, seen[j], tb.sections[j])
			}
		}
	}
}
