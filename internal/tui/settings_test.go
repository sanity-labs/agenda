package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

func press(r rune) tea.KeyPressMsg   { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func special(c rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: c} }

func TestSettingsTableRoundTrips(t *testing.T) {
	cfg := config.Default()
	for _, s := range settingsTable() {
		if s.kind == kindHeader {
			continue
		}
		if s.path == "" || s.get == nil || s.set == nil {
			t.Errorf("setting %q missing path/get/set", s.label)
			continue
		}
		switch s.kind {
		case kindBool:
			s.set(&cfg, "on")
			if got := s.get(cfg); got != "on" {
				t.Errorf("%s: set on, get %q", s.path, got)
			}
			s.set(&cfg, "off")
			if got := s.get(cfg); got != "off" {
				t.Errorf("%s: set off, get %q", s.path, got)
			}
		case kindEnum:
			opts := s.options()
			if len(opts) == 0 {
				t.Errorf("%s: enum with no options", s.path)
				continue
			}
			s.set(&cfg, opts[len(opts)-1])
			if got := s.get(cfg); got != opts[len(opts)-1] {
				t.Errorf("%s: set %q, get %q", s.path, opts[len(opts)-1], got)
			}
		case kindText:
			s.set(&cfg, "7m")
			if got := s.get(cfg); got != "7m" {
				t.Errorf("%s: set 7m, get %q", s.path, got)
			}
		case kindNum:
			s.set(&cfg, "37")
			if got := s.get(cfg); got != "37" {
				t.Errorf("%s: set 37, get %q", s.path, got)
			}
		}
	}
}

func TestOverlayToggleAndCycle(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()

	// First selectable row is the theme enum; cycling right moves off default.
	change, closed := o.Update(press(' '), cfg)
	if closed || change == nil {
		t.Fatal("space on the enum row should commit a cycle")
	}
	if change.s.path != "theme.name" || change.val == "default" {
		t.Errorf("change = %s -> %q, want theme.name to a non-default palette", change.s.path, change.val)
	}
	// Cycling left from default wraps to the last palette.
	change, _ = o.Update(special(tea.KeyLeft), cfg)
	if change == nil || change.val == "default" {
		t.Errorf("left from default should wrap, got %+v", change)
	}

	// Navigate down to the first bool (notifications enabled) and toggle it.
	for {
		o.Update(press('j'), cfg)
		if o.rows[o.cursor].kind == kindBool {
			break
		}
	}
	before := o.rows[o.cursor].get(cfg) == "on"
	change, _ = o.Update(special(tea.KeyEnter), cfg)
	if change == nil || change.fileValue() != !before {
		t.Fatalf("toggling a bool should commit its inverse (was %v), got %+v", before, change)
	}
}

func TestOverlayTextEditValidation(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	// Move to the first text row (refresh every).
	for o.rows[o.cursor].kind != kindText {
		o.Update(press('j'), cfg)
	}
	o.Update(special(tea.KeyEnter), cfg) // start editing
	if !o.editing {
		t.Fatal("enter on a text row should start editing")
	}
	for _, r := range "bogus" {
		o.Update(press(r), cfg)
	}
	if change, _ := o.Update(special(tea.KeyEnter), cfg); change != nil || o.errMsg == "" {
		t.Fatal("committing an invalid duration should error and stay open")
	}
	// esc cancels, then a valid value commits.
	o.Update(special(tea.KeyEscape), cfg)
	o.Update(special(tea.KeyEnter), cfg)
	for _, r := range "5m" {
		o.Update(press(r), cfg)
	}
	change, _ := o.Update(special(tea.KeyEnter), cfg)
	if change == nil || change.val != "5m" || change.s.path != "refresh.every" {
		t.Fatalf("change = %+v, want refresh.every 5m", change)
	}
}

func TestOverlayNavigationSkipsHeaders(t *testing.T) {
	cfg := config.Default()
	o := newConfigOverlay()
	for i := 0; i < len(o.rows)*2; i++ {
		if o.rows[o.cursor].kind == kindHeader {
			t.Fatalf("cursor landed on header %q", o.rows[o.cursor].label)
		}
		o.Update(press('j'), cfg)
	}
}

// Every config option should be reachable from ctrl+s: a setting only in the
// YAML is one most people never find. Container keys and free-text fields
// (tokens, search queries, keymaps) are deliberately file-only.
func TestEveryConfigKeyHasASettingRow(t *testing.T) {
	fileOnly := map[string]bool{
		// Containers, not settings in themselves.
		"github": true, "linear": true, "sessions": true, "theme": true,
		"refresh": true, "notifications": true, "keys": true, "filter": true,
		// Free text, edited in the file.
		"token": true, "review_filter": true, "views": true, "palette": true,
		"glyphs": true, "command": true, "limit": true, "scope": true,
		"teams": true, "projects": true, "states": true, "nav": true,
		"include_completed": true, "include_canceled": true,
	}
	src, err := os.ReadFile("../config/config.go")
	if err != nil {
		t.Fatal(err)
	}
	rows := settingsTable()
	for _, m := range regexp.MustCompile(`yaml:"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		key := m[1]
		if fileOnly[key] {
			continue
		}
		found := false
		for _, r := range rows {
			if r.path == key || strings.HasSuffix(r.path, "."+key) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("config key %q has no row in the ctrl+s overlay; add one or list it as file-only", key)
		}
	}
}
