package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Labels fill the space they are given, and what does not fit becomes a
// count so the row still says labels exist.
func TestFitLabels(t *testing.T) {
	pills := []string{"[infrastructure]", "[needs-review]", "[sre]"}
	for _, c := range []struct {
		budget   int
		wantPill string
		wantMark string
	}{
		{0, "", ""},
		{4, "", "+3"}, // no pill fits: the count alone
		{22, "infrastructure", "+2"},
		{60, "sre", ""}, // all three, no marker
	} {
		got := FitLabels(pills, c.budget)
		if w := lipgloss.Width(got); c.budget > 0 && w > c.budget {
			t.Errorf("budget %d: rendered %d wide (%q)", c.budget, w, got)
		}
		if c.wantPill != "" && !strings.Contains(got, c.wantPill) {
			t.Errorf("budget %d: %q missing %q", c.budget, got, c.wantPill)
		}
		if c.wantMark != "" && !strings.Contains(got, c.wantMark) {
			t.Errorf("budget %d: %q missing marker %q", c.budget, got, c.wantMark)
		}
		if c.wantMark == "" && c.budget >= 60 && strings.Contains(got, "+") {
			t.Errorf("budget %d: %q marked overflow but everything fit", c.budget, got)
		}
	}
}

// A row whose only label is too wide must not look like a row with none.
func TestFitLabelsCountsWhenNothingFits(t *testing.T) {
	got := FitLabels([]string{"[a-very-long-label-name]"}, 10)
	if got != LabelOverflow(1) {
		t.Errorf("FitLabels = %q, want the overflow marker %q", got, LabelOverflow(1))
	}
	// Too narrow for even the marker: nothing, rather than a stray glyph.
	if got := FitLabels([]string{"[x]"}, 1); got != "" {
		t.Errorf("FitLabels with no room = %q, want empty", got)
	}
}

// PadCell measures display width, or ANSI and wide glyphs break alignment.
func TestPadCellMeasuresDisplayWidth(t *testing.T) {
	coloured := Green.Render("+7")
	if w := lipgloss.Width(PadCell(coloured, 9)); w != 9 {
		t.Errorf("padded coloured cell is %d wide, want 9", w)
	}
	// Already wider than the cell: left alone rather than truncated.
	long := "+12345 -67890"
	if got := PadCell(long, 5); got != long {
		t.Errorf("PadCell shortened an over-wide cell: %q", got)
	}
}
