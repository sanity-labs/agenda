package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
)

func restartRow(t *testing.T, o *configOverlay, path string) *setting {
	t.Helper()
	for i := range o.rows {
		if o.rows[i].path == path {
			if o.rows[i].note != "restart" {
				t.Fatalf("%s is not a restart row", path)
			}
			return &o.rows[i]
		}
	}
	t.Fatalf("no row for %s", path)
	return nil
}

// A restart-marked change is pending until applied: the overlay says so and
// offers r; leaving warns once, and the next esc reverts the change in the
// live config rather than leaving the file ahead of the running app.
func TestRestartRowsWarnThenRevert(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m.settings = newConfigOverlay()
	row := restartRow(t, m.settings, "github.diff_pane")

	m.commitSetting(&settingChange{s: row, val: "on"})
	if !m.cfg.GitHub.DiffPane {
		t.Fatal("setup: the change did not apply to the live config")
	}
	if view := ansi.Strip(m.settings.View(m.cfg)); !strings.Contains(view, "r reload") {
		t.Errorf("the overlay does not offer a reload with a change pending:\n%s", view)
	}

	got, _ := m.Update(special(tea.KeyEscape))
	m = got.(Model)
	if m.settings == nil {
		t.Fatal("the first esc closed the overlay without warning")
	}
	if view := ansi.Strip(m.settings.View(m.cfg)); !strings.Contains(view, "Unapplied") {
		t.Errorf("the first esc did not warn:\n%s", view)
	}

	got, _ = m.Update(special(tea.KeyEscape))
	m = got.(Model)
	if m.settings != nil {
		t.Fatal("the second esc did not close the overlay")
	}
	if m.cfg.GitHub.DiffPane {
		t.Error("the second esc did not revert the pending change")
	}
	if m.Restart() {
		t.Error("reverting asked for a restart")
	}
}

// r with a change pending quits for a restart; changing a row back to its
// original value leaves nothing pending.
func TestRestartRowsReloadOnR(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m.settings = newConfigOverlay()
	row := restartRow(t, m.settings, "github.diff_pane")

	m.commitSetting(&settingChange{s: row, val: "on"})
	m.commitSetting(&settingChange{s: row, val: "off"})
	if len(m.settings.pending) != 0 {
		t.Errorf("changing a row back left it pending: %v", m.settings.pending)
	}
	got, cmd := m.Update(special('r'))
	m = got.(Model)
	if m.settings == nil || cmd != nil {
		t.Fatal("r with nothing pending should do nothing")
	}

	m.commitSetting(&settingChange{s: row, val: "on"})
	got, cmd = m.Update(special('r'))
	m = got.(Model)
	if m.settings != nil || cmd == nil {
		t.Fatal("r with a change pending did not close the overlay with a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("r did not quit the program for the restart")
	}
	if !m.Restart() {
		t.Error("the model does not report the restart main should perform")
	}
}
