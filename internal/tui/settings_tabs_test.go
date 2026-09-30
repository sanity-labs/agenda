package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
