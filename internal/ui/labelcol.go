package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Label column geometry, shared by the views that render one.
const (
	// LabelColMargin keeps the column clear of the metadata on its left and
	// the cells on its right, so nothing reads as one run of text.
	LabelColMargin = 2
	// LabelColMaxWidth caps the column so labels cannot dominate a very
	// wide terminal at the title's expense.
	LabelColMaxWidth = 40
	// LabelColMinRow is the narrowest list that gets a label column at all;
	// below it the metadata needs every column.
	LabelColMinRow = 110
)

// PadCell left-aligns s in w columns, measuring display width so ANSI and
// wide glyphs do not throw the padding off.
func PadCell(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// LabelOverflow is the "+2" that stands for labels with no room.
func LabelOverflow(n int) string { return Faint.Render(fmt.Sprintf("+%d", n)) }

// FitLabels packs rendered pills into budget columns, replacing whatever
// does not fit with a "+N" so the row says how many are hidden rather than
// dropping them silently. pills are pre-rendered, one per label, in order.
func FitLabels(pills []string, budget int) string {
	if len(pills) == 0 || budget <= 0 {
		return ""
	}
	var out []string
	used := 0
	for i, pill := range pills {
		w := lipgloss.Width(pill)
		gap := 0
		if len(out) > 0 {
			gap = 1
		}
		// Keep room for the overflow marker while labels remain, or the
		// last pill fits and the "+N" then has nowhere to go.
		spare := 0
		if i < len(pills)-1 {
			spare = 1 + lipgloss.Width(LabelOverflow(len(pills)-i-1))
		}
		if used+gap+w+spare > budget {
			break
		}
		out = append(out, pill)
		used += gap + w
	}
	// Nothing fit, but the row does have labels: say how many rather than
	// looking like a row with none. A truncated pill would misread as a
	// different label, so the count is the honest option.
	if len(out) == 0 {
		marker := LabelOverflow(len(pills))
		if lipgloss.Width(marker) > budget {
			return ""
		}
		return marker
	}
	if hidden := len(pills) - len(out); hidden > 0 {
		out = append(out, LabelOverflow(hidden))
	}
	return strings.Join(out, " ")
}

// LabelColWidth is the label column's width for a row of this width, given
// what the row's own cells and metadata already need.
func LabelColWidth(rowWidth, cellsWidth, metaReserve int) int {
	avail := rowWidth - cellsWidth - LabelColMargin*2 - metaReserve
	return min(avail, LabelColMaxWidth)
}
