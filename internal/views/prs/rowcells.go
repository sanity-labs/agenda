package prs

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"

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

// labelColReserve is the room left for the metadata line itself, so labels
// cannot crowd out the repo, number, author and branch.
const labelColReserve = 46

// labelColMinWidth is this view's threshold for showing labels at all.
const labelColMinWidth = ui.LabelColMinRow

// rightCluster is the row's right-hand side: an optional label column, then
// the diff, comment and age cells at fixed widths. dim renders the numbers
// faint, for rows already reviewed.
func (p pr) rightCluster(width int, dim bool) string {
	age := ui.Age(p.UpdatedAt)
	diff, comments := p.diffText(), p.commentsText()

	// Do NOT trim the padding: the row renderer right-aligns this cluster
	// against the row width, so a cluster that shrinks with its contents
	// puts every cell at a different column. Constant width is what turns
	// right-alignment into fixed columns.
	var nums string
	if dim {
		nums = ui.Dim.Render(ui.PadCell(diff, diffCellW) + " " +
			ui.PadCell(comments, commentsCellW) + " " +
			ui.PadCell(age, ageCellW))
	} else {
		nums = ui.PadCell(p.diffCell(), diffCellW) + " " +
			ui.PadCell(p.commentsCell(), commentsCellW) + " " +
			ui.PadCell(ui.Dim.Render(age), ageCellW)
	}

	if !p.ShowLabels {
		return nums
	}
	// A fixed-width column, so labels begin at the same place on every row
	// rather than drifting with the numbers they sit beside. The cluster is
	// right-aligned by the row renderer, and the numeric cells are fixed,
	// so padding the labels to a constant width pins the whole block.
	cells := diffCellW + commentsCellW + ageCellW + 2 // the cell gaps
	budget := ui.LabelColWidth(width, cells, labelColReserve)
	if budget <= 0 {
		return nums
	}
	labels := ui.FitLabels(pillsFor(p.Labels.Nodes), budget)
	return ui.PadCell(labels, budget) + strings.Repeat(" ", ui.LabelColMargin) + nums
}

// pillsFor renders one pill per label, for the shared column packer.
func pillsFor(labels []label) []string {
	out := make([]string, len(labels))
	for i := range labels {
		out[i] = labelPills(labels[i : i+1])
	}
	return out
}

// effectiveQuery is the search actually in force for the section in view:
// the configured filter, plus the terms the enabled settings imply.
// Surfaced because the two combine invisibly otherwise, and "why is this
// PR missing" is then unanswerable from the screen.
//
// The typed filter is not included: it already shows on the filter line,
// and repeating it would say the same thing twice.
func (v *View) effectiveQuery() string {
	q := strings.TrimSpace(v.cfg.Filter)
	if v.showReview && strings.TrimSpace(v.cfg.ReviewFilter) != "" {
		// Two searches run, so show both rather than implying one.
		q += "  +  " + strings.TrimSpace(v.cfg.ReviewFilter)
	}
	if v.hideApproved {
		q += " -review:approved"
	}
	return strings.TrimSpace(q)
}

// queryBox renders a query in a bordered box of the given width, the way
// gh-dash shows its search: the border is what makes it read as the query
// in force rather than another status line.
func queryBox(q string, width int) string {
	// Width() counts the border and padding inside the width it is given,
	// and the Nerd Font magnifier is two columns, not one. Measure both
	// rather than assume, or the query runs a column long and wraps the
	// box to four rows.
	icon := ui.Glyph(ui.IconSearch, "?")
	lead := icon + " "
	boxW := width - 2
	text := max(1, boxW-queryBoxChrome-lipgloss.Width(lead))
	body := ui.Faint.Render(lead) + ui.Faint.Italic(true).Render(
		ansi.Truncate(q, text, "…"))
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Border)).
		Padding(0, 1).
		Width(boxW).
		Render(body)
}

const (
	// queryBoxChrome is the border (2) and padding (2) Width() counts.
	queryBoxChrome = 4
	// queryBoxMin is the narrowest list that gets a box: below it the two
	// border rows cost more than the query is worth.
	queryBoxMin = 30
)
