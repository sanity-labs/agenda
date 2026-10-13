package tui

import (
	"charm.land/lipgloss/v2"
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The whole path: a view raises a warning, the status row shows the summary,
// and an error opens the detail overlay.
func TestStatusRowAndOverlay(t *testing.T) {
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()

	warn := ui.Status(ui.SeverityWarn, "PRs", "3 of 4 hidden: `org` forbids classic PATs",
		"Unset GITHUB_TOKEN so gh uses its own login.")
	got, _ := m.Update(warn)
	m = got.(Model)

	// No permanent row: the toast announces it and the footer says one is
	// waiting, so a third copy would sit there until the log was opened.
	if line := m.statusLine(); line != "" {
		t.Errorf("a warning left a permanent status row: %q", line)
	}
	if m.statusHeight() != 0 {
		t.Errorf("statusHeight = %d, want 0 with no status row", m.statusHeight())
	}
	if m.statusOpen {
		t.Error("a warning opened the overlay; only errors should")
	}

	// The footer points at it instead.
	hint := m.issuesHint()
	if !strings.Contains(hint, "errors") {
		t.Errorf("footer hint = %q, want it to say errors", hint)
	}
	if !strings.Contains(hint, m.keys.Messages.Keys()[0]) {
		t.Errorf("footer hint = %q, want the key that opens the log", hint)
	}

	// An error opens the overlay on arrival.
	got, _ = m.Update(ui.Status(ui.SeverityError, "PRs", "fetch failed", "gh: not authenticated"))
	m = got.(Model)
	if !m.statusOpen {
		t.Error("an error did not open the detail overlay")
	}
	detail := ui.DetailView(m.status, 70)
	if !strings.Contains(detail, "gh: not authenticated") {
		t.Errorf("overlay missing the detail:\n%s", detail)
	}
	if !strings.Contains(detail, "3 of 4 hidden") {
		t.Error("overlay lost the earlier message; it should keep history")
	}
}

// A message changes the layout height, so the frame and preview caches from
// the scroll work must not survive it.
func TestStatusMessageInvalidatesCaches(t *testing.T) {
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	_ = m.View()
	if m.frameDirty() {
		t.Fatal("frame not cached after a draw")
	}
	before := m.contentHeight()

	got, _ := m.Update(ui.Status(ui.SeverityWarn, "PRs", "3 hidden", ""))
	m = got.(Model)
	if !m.frameDirty() {
		t.Error("frame reused after a message arrived")
	}
	// The height no longer changes (there is no status row), but the frame
	// still has to redraw: the footer gains its errors marker.
	if m.contentHeight() != before {
		t.Errorf("contentHeight %d -> %d, want it unchanged", before, m.contentHeight())
	}
	if !strings.Contains(m.renderFooter(), "errors") {
		t.Error("the footer does not show the waiting message")
	}
}

// With the hotkey bar off only the essentials remain, on the right.
func TestFooterHiddenKeepsErrorsAndHelp(t *testing.T) {
	cfg := config.Default()
	off := false
	cfg.Footer = &off
	m := New(cfg, []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()

	footer := m.renderFooter()
	if strings.Contains(footer, "sort") || strings.Contains(footer, "refresh") {
		t.Errorf("the hidden footer still lists hotkeys:\n%s", footer)
	}
	if !strings.Contains(footer, "help") {
		t.Error("the hidden footer dropped the help key")
	}
	if strings.Contains(footer, "errors") {
		t.Error("the footer says errors with none waiting")
	}

	got, _ := m.Update(ui.Status(ui.SeverityWarn, "PRs", "3 hidden", "why"))
	m = got.(Model)
	footer = m.renderFooter()
	if !strings.Contains(footer, "errors") {
		t.Errorf("the hidden footer does not surface a waiting message:\n%s", footer)
	}
	// Still one row, so the layout arithmetic is unchanged either way.
	if h := lipgloss.Height(footer); h != footerHeight {
		t.Errorf("the hidden footer is %d rows, want %d", h, footerHeight)
	}
}

// A message that arrives again and again (a forbidden search on every
// refresh) is one log entry with a count and the latest time, not a wall
// of repeats; the footer counts it once.
func TestRepeatedStatusFoldsIntoOneEntry(t *testing.T) {
	m := New(config.Default(), []View{&fatView{title: "x"}})
	m.width, m.height, m.ready = 100, 30, true
	m.layout()
	warn := ui.Status(ui.SeverityWarn, "PRs", "1 of 6 hidden: `org` forbids classic PATs", "use a fine-grained token")
	for i := 0; i < 5; i++ {
		got, _ := m.Update(warn)
		m = got.(Model)
	}
	got, _ := m.Update(ui.Status(ui.SeverityError, "Linear", "fetch failed", ""))
	m = got.(Model)
	got, _ = m.Update(warn)
	m = got.(Model)
	if len(m.status) != 2 {
		t.Fatalf("log has %d entries, want 2 (one folded warning, one error)", len(m.status))
	}
	last := m.status[len(m.status)-1]
	if last.Summary != warn.Summary || last.Count != 6 {
		t.Fatalf("latest entry = %q ×%d, want the warning ×6 moved to the end", last.Summary, last.Count)
	}
	if m.unreadIssues() != 2 {
		t.Errorf("footer counts %d issues, want 2", m.unreadIssues())
	}
	if !strings.Contains(ui.DetailView(m.status, 70), "×6") {
		t.Error("the overlay does not show the repeat count")
	}
}
