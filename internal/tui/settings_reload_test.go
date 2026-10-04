package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func restartRow(t *testing.T, o *configOverlay, path string) *setting {
	t.Helper()
	for i := range o.rows {
		if o.rows[i].path == path {
			if o.rows[i].note != noteReload {
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
	view := ansi.Strip(m.settings.View(m.cfg))
	if !strings.Contains(view, "unapplied option") || strings.Contains(view, "tab section") {
		t.Errorf("the first esc did not collapse the panel to the warning box:\n%s", view)
	}
	// Any other key goes back to the panel; esc again is what reverts.
	got, _ = m.Update(special(tea.KeyDown))
	m = got.(Model)
	if m.settings == nil || m.settings.warned {
		t.Fatal("a stray key did not return to the panel")
	}
	got, _ = m.Update(special(tea.KeyEscape))
	m = got.(Model)

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
	if !strings.HasPrefix(m.RestartState(), "panel:") {
		t.Errorf("reload from the panel should ask the next process to reopen it, got %q", m.RestartState())
	}
}

// The reloaded process picks up where the last one left off: the panel
// reopens on the same tab (not after the leave warning, which closed it)
// and a green toast says the reload went through.
func TestReloadedProcessReopensThePanelAndConfirms(t *testing.T) {
	cfg := config.Default()
	cfg.Refresh.Every = 0 // no tick commands to wait on
	m := New(cfg, []View{&stubView{"PRs"}}).WithReloaded("panel:3")
	if m.settings == nil || m.settings.tab != 3 {
		t.Fatalf("panel not reopened on tab 3: settings=%v", m.settings != nil)
	}
	if !hasSuccessToast(t, m.Init()) {
		t.Error("no success toast after a reload from the panel")
	}

	w := New(cfg, []View{&stubView{"PRs"}}).WithReloaded("warning")
	if w.settings != nil {
		t.Error("a reload from the leave warning reopened the panel")
	}
	if !hasSuccessToast(t, w.Init()) {
		t.Error("no success toast after a reload from the warning")
	}
	if New(cfg, []View{&stubView{"PRs"}}).WithReloaded("").settings != nil {
		t.Error("a plain launch opened the settings")
	}
}

// hasSuccessToast runs the command tree and reports a green toast among the
// messages, giving each leaf a moment so a stray timer cannot hang the test.
func hasSuccessToast(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	var walk func(tea.Cmd) bool
	walk = func(c tea.Cmd) bool {
		if c == nil {
			return false
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			switch v := msg.(type) {
			case tea.BatchMsg:
				for _, sub := range v {
					if walk(sub) {
						return true
					}
				}
			case ui.ToastMsg:
				return v.Success && strings.Contains(v.Body, "Reload successful")
			}
		case <-time.After(200 * time.Millisecond):
		}
		return false
	}
	return walk(cmd)
}
