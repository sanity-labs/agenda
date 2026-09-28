package tui

import (
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

	line := m.statusLine()
	if !strings.Contains(line, "3 of 4 hidden") {
		t.Errorf("status row = %q, want the summary", line)
	}
	if !strings.Contains(line, "details") {
		t.Errorf("status row = %q, want the details hint", line)
	}
	if m.statusOpen {
		t.Error("a warning opened the overlay; only errors should")
	}

	// A warning leaves room for the body by taking one row.
	if m.statusHeight() != 1 {
		t.Errorf("statusHeight = %d, want 1 while a message shows", m.statusHeight())
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
