package prs

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// Fixed widths for the right-hand cells. They were sized to their content,
// so a row with 4-digit diffs shifted every neighbouring column and the
// eye had nothing to track down the list. Padded to these instead, which
// is what makes them left-aligned and stable.
const (
	diffCellW     = 11 // "+45k -9.0k", the widest the compact units produce
	commentsCellW = 4  // icon + up to 3 digits
	ageCellW      = 4  // "999d"
)

// compactCount renders n in at most 4 characters, switching unit rather
// than letting a big number widen the column: 1234 -> "1.2k", 45678 ->
// "46k". Exactness matters less here than a column that holds still.
func compactCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 9950:
		// Stop below the rounding boundary: 9999 formats as "10.0k", five
		// wide, which breaks the cell.
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 999500:
		// Round rather than truncate: 9999 reading as "9k" understates it
		// by a thousand.
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%dM", (n+500000)/1000000)
	}
}

// padCell left-aligns s in w columns, measuring display width so ANSI and
// wide glyphs do not throw the padding off.
func padCell(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// diffText is the +/- cell's plain text, unit-converted.
func (p pr) diffText() string {
	if p.Additions == 0 && p.Deletions == 0 {
		return ""
	}
	return "+" + compactCount(p.Additions) + " -" + compactCount(p.Deletions)
}

// commentsText is the comment-count cell's plain text.
func (p pr) commentsText() string {
	if p.Comments.TotalCount == 0 {
		return ""
	}
	return ui.IconComment + compactCount(p.Comments.TotalCount)
}

// rowLabels renders label pills to fit budget columns, replacing whatever
// does not fit with a "+N" so the row says how many are hidden rather than
// silently dropping them. Empty when there is no room for even one.
func rowLabels(labels []label, budget int) string {
	if len(labels) == 0 || budget <= 0 {
		return ""
	}
	var out []string
	used := 0
	for i := range labels {
		pill := labelPills(labels[i : i+1])
		w := lipgloss.Width(pill)
		gap := 0
		if len(out) > 0 {
			gap = 1
		}
		// Keep room for the overflow marker while labels remain, or the
		// last pill fits and the "+N" then has nowhere to go.
		spare := 0
		if i < len(labels)-1 {
			spare = 1 + lipgloss.Width(overflowMarker(len(labels)-i-1))
		}
		if used+gap+w+spare > budget {
			break
		}
		out = append(out, pill)
		used += gap + w
	}
	// Nothing fit, but the row does have labels: say how many rather than
	// looking like a PR with none. A truncated pill would misread as a
	// different label, so the count is the honest option.
	if len(out) == 0 {
		marker := overflowMarker(len(labels))
		if lipgloss.Width(marker) > budget {
			return ""
		}
		return marker
	}
	if hidden := len(labels) - len(out); hidden > 0 {
		out = append(out, overflowMarker(hidden))
	}
	return strings.Join(out, " ")
}

// overflowMarker is the "+2" that stands for labels with no room.
func overflowMarker(n int) string {
	return ui.Faint.Render(fmt.Sprintf("+%d", n))
}

// Margins keep the label column clear of the metadata on its left and the
// numeric cells on its right, so nothing ever reads as one run of text.
const (
	labelColMargin = 2
	// labelColReserve is the room left for the metadata line itself, so
	// labels cannot crowd out the repo, number, author and branch.
	labelColReserve = 46
	// labelColMinWidth is the narrowest list that gets a label column at
	// all; below it the metadata needs every column.
	labelColMinWidth = 110
	// labelColMaxWidth caps the column so labels cannot dominate a very
	// wide terminal at the title's expense.
	labelColMaxWidth = 40
)

// rightCluster is the row's right-hand side: an optional label column, then
// the diff, comment and age cells at fixed widths. dim renders the numbers
// faint, for rows already reviewed.
func (p pr) rightCluster(width int, dim bool) string {
	age := ui.Age(p.UpdatedAt)
	diff, comments := p.diffText(), p.commentsText()

	var nums string
	if dim {
		nums = padCell(diff, diffCellW) + " " +
			padCell(comments, commentsCellW) + " " +
			padCell(age, ageCellW)
		nums = ui.Dim.Render(strings.TrimRight(nums, " "))
	} else {
		nums = padCell(p.diffCell(), diffCellW) + " " +
			padCell(p.commentsCell(), commentsCellW) + " " +
			padCell(ui.Dim.Render(age), ageCellW)
		nums = strings.TrimRight(nums, " ")
	}

	if !p.ShowLabels {
		return nums
	}
	// A fixed-width column, so labels begin at the same place on every row
	// rather than drifting with the numbers they sit beside. The cluster is
	// right-aligned by the row renderer, and the numeric cells are fixed,
	// so padding the labels to a constant width pins the whole block.
	budget := labelColWidth(width)
	if budget <= 0 {
		return nums
	}
	labels := rowLabels(p.Labels.Nodes, budget)
	return padCell(labels, budget) + strings.Repeat(" ", labelColMargin) + nums
}

// labelColWidth is the label column's width for a row of this width: what
// is left after the numeric cells, the margins, and the room the metadata
// needs, capped so labels never take more than their share.
func labelColWidth(width int) int {
	spent := diffCellW + commentsCellW + ageCellW + 2 // the cell gaps
	avail := width - spent - labelColMargin*2 - labelColReserve
	return min(avail, labelColMaxWidth)
}
