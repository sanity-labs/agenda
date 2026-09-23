package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TwoLineRow renders a list item as a two-line block, à la gh-dash's
// non-compact layout:
//
//	▌  <glyphs>  <meta….……….……….……….….>  <right>
//	            <title, bold>
//
// Line one is the glyphs followed by dimmed metadata, with `right` pinned to
// the far edge; line two is the title, indented to align under the metadata.
// The selection accent bar spans both lines (rather than a full-row background,
// which lipgloss's per-segment resets would clobber).
//
// metaStyled is the colored metadata; metaPlain is the same text uncolored,
// used for width measurement and safe truncation (so we never cut through an
// ANSI escape). glyphs and right may contain styling; their display width is
// measured with lipgloss.Width.
func TwoLineRow(width int, selected bool, glyphs, metaPlain, metaStyled, right, title string, hl Highlighter) string {
	return twoLineRow(width, selected, glyphs, metaPlain, metaStyled, right, title, hl, false)
}

// TwoLineRowFaint is TwoLineRow with the title rendered faint — for rows the
// user has already dealt with and should be able to skim past. The dim is
// uniform even when selected: the accent bar alone marks the cursor.
func TwoLineRowFaint(width int, selected bool, glyphs, metaPlain, metaStyled, right, title string, hl Highlighter) string {
	return twoLineRow(width, selected, glyphs, metaPlain, metaStyled, right, title, hl, true)
}

func twoLineRow(width int, selected bool, glyphs, metaPlain, metaStyled, right, title string, hl Highlighter, faint bool) string {
	bar := "  "
	if selected {
		bar = Accent.Render("▌") + " "
	}

	prefix := bar + glyphs + "  "
	indent := lipgloss.Width(prefix)

	avail := max(1, width-indent-lipgloss.Width(right)-1)
	meta := metaStyled
	if lipgloss.Width(metaPlain) > avail {
		// Escape-aware truncation keeps the per-segment colors; a plain
		// truncate would have to drop to a single dim style.
		meta = ansi.Truncate(metaStyled, avail, "…")
	}
	gap := max(1, width-indent-lipgloss.Width(meta)-lipgloss.Width(right))
	line1 := prefix + meta + strings.Repeat(" ", gap) + right

	plainTitle := Truncate(title, max(1, width-indent))
	t := hl.Highlight(plainTitle)
	switch {
	case faint:
		t = Faint.Render(t)
	case selected:
		t = Bold.Render(t)
	default:
		t = Text.Render(t)
	}
	line2 := bar + strings.Repeat(" ", indent-lipgloss.Width(bar)) + t

	return line1 + "\n" + line2
}

// SectionSeparator draws a section header as a two-line block (blank line +
// full-width reverse-video band), sized for the two-line row layout every
// view uses.
//
// A band rather than a labeled rule: GroupHeader already draws a thin rule,
// and a section boundary has to read as a different *kind* of object, not a
// brighter version of the same one, or the eye files the two lists as one
// list with a line through it. A filled line is the one shape no row can
// produce, so it survives peripheral vision.
func SectionSeparator(label string, width int) string {
	if width < 1 {
		return "\n"
	}
	// Truncate the padded label as a whole, so a very narrow pane clips the
	// band instead of overflowing it and breaking the two-line row height.
	text := Truncate(" "+label+" ", width)
	pad := max(0, width-lipgloss.Width(text))
	return "\n" + Band.Render(text+strings.Repeat(" ", pad))
}
