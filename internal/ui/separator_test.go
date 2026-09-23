package ui

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// sepItem is a strItem that can also be a non-selectable separator.
type sepItem struct {
	text string
	sep  bool
}

func (s sepItem) Render(int, bool, Highlighter) string { return s.text }
func (s sepItem) Fields() []Field {
	if s.sep {
		return nil
	}
	return []Field{{Name: "text", Text: s.text}}
}
func (s sepItem) Filter() string   { return s.text }
func (s sepItem) Selectable() bool { return !s.sep }

func sepList(items ...sepItem) List[sepItem] {
	l := NewList[sepItem]()
	l.SetItems(items)
	l.SetSize(40, 10)
	return l
}

func TestSeparatorNavigationSkips(t *testing.T) {
	l := sepList(
		sepItem{text: "a"},
		sepItem{text: "-- sep --", sep: true},
		sepItem{text: "b"},
	)
	l.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if got := l.Selected().text; got != "b" {
		t.Errorf("down over separator selected %q, want b", got)
	}
	l.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if got := l.Selected().text; got != "a" {
		t.Errorf("up over separator selected %q, want a", got)
	}
}

func TestSeparatorNeverSelectedAtEdges(t *testing.T) {
	l := sepList(
		sepItem{text: "-- top --", sep: true},
		sepItem{text: "a"},
		sepItem{text: "b"},
		sepItem{text: "-- bottom --", sep: true},
	)
	if got := l.Selected().text; got != "a" {
		t.Errorf("initial selection = %q, want a (skipping leading separator)", got)
	}
	l.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if got := l.Selected().text; got != "b" {
		t.Errorf("G selected %q, want b (skipping trailing separator)", got)
	}
	l.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if got := l.Selected().text; got != "a" {
		t.Errorf("g selected %q, want a", got)
	}
}

func TestSeparatorDroppedWhileFiltering(t *testing.T) {
	l := sepList(
		sepItem{text: "alpha"},
		sepItem{text: "-- sep --", sep: true},
		sepItem{text: "beta"},
	)
	l.SetQuery("e") // matches "sep" label too if separators weren't dropped
	if l.Len() != 1 {
		t.Fatalf("filtered Len = %d, want 1 (beta only, no separator)", l.Len())
	}
	if got := l.Selected().text; got != "beta" {
		t.Errorf("filtered selection = %q, want beta", got)
	}
}

func TestGroupedFieldNamesSkipHeaders(t *testing.T) {
	l := sepList(
		sepItem{text: "-- lane --", sep: true},
		sepItem{text: "a"},
	)
	names := l.FieldNames()
	if len(names) != 1 || names[0] != "text" {
		t.Errorf("FieldNames() = %v, want [text] from the first real item", names)
	}
}

func TestJumpKeepsHeaderVisible(t *testing.T) {
	items := []sepItem{{text: "-- top --", sep: true}}
	for i := 0; i < 12; i++ {
		items = append(items, sepItem{text: string(rune('a' + i))})
	}
	l := sepList(items...)
	l.SetSize(40, 5) // window smaller than the list

	l.Update(tea.KeyPressMsg{Code: 'G', Text: "G"}) // bottom
	l.Update(tea.KeyPressMsg{Code: 'g', Text: "g"}) // back to top
	if got := l.Selected().text; got != "a" {
		t.Fatalf("g selected %q, want a", got)
	}
	if l.offset != 0 {
		t.Errorf("offset = %d after jump to top, want 0 so the lane header shows", l.offset)
	}
}

// The section band is one line of solid background (plus the leading blank
// that keeps every separator two rows tall), padded to the full list width so
// it reads as a band rather than a label with a rule after it.
func TestSectionSeparatorBandFillsWidth(t *testing.T) {
	const width = 40
	out := SectionSeparator("REVIEW REQUESTED  ·  3", width)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 for a row height of 2", len(lines))
	}
	if lines[0] != "" {
		t.Errorf("first line = %q, want blank breathing room", lines[0])
	}
	if got := lipgloss.Width(lines[1]); got != width {
		t.Errorf("band width = %d, want the full %d", got, width)
	}
	if !strings.Contains(lines[1], "REVIEW REQUESTED") {
		t.Errorf("band %q lost its label", lines[1])
	}
	// Reverse video, not a plain foreground color: the label sits inside the
	// band, so the fill color comes from the terminal swapping fg and bg.
	if !slices.Contains(sgrParams(lines[1]), "7") {
		t.Errorf("band %q is not reverse video", lines[1])
	}
}

// sgrParams returns the numeric parameters of the first SGR escape sequence
// in s. lipgloss merges attributes into one sequence ("\x1b[1;7;95m"), so a
// test can't look for a bare "\x1b[7m".
func sgrParams(s string) []string {
	m := regexp.MustCompile(`\x1b\[([0-9;]*)m`).FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	return strings.Split(m[1], ";")
}

// A band narrower than its label truncates instead of wrapping, which would
// break the fixed two-line row height.
func TestSectionSeparatorBandTruncates(t *testing.T) {
	for _, width := range []int{0, 1, 8, 12} {
		out := SectionSeparator("REVIEW REQUESTED  ·  3", width)
		lines := strings.Split(out, "\n")
		if len(lines) != 2 {
			t.Errorf("width %d: got %d lines, want 2", width, len(lines))
			continue
		}
		if got := lipgloss.Width(lines[1]); got > max(width, 0) {
			t.Errorf("width %d: band is %d wide, want no overflow", width, got)
		}
	}
}

// A section band stacked directly above a lane header is what the PR view
// produces with grouping on, so a jump to the top has to pull both into view,
// not just the nearest one.
func TestJumpKeepsStackedHeadersVisible(t *testing.T) {
	items := []sepItem{
		{text: "-- band --", sep: true},
		{text: "-- lane --", sep: true},
	}
	for i := 0; i < 12; i++ {
		items = append(items, sepItem{text: string(rune('a' + i))})
	}
	l := sepList(items...)
	l.SetSize(40, 6)

	l.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	l.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if got := l.Selected().text; got != "a" {
		t.Fatalf("g selected %q, want a", got)
	}
	if l.offset != 0 {
		t.Errorf("offset = %d, want 0 so both the band and the lane header show", l.offset)
	}
}
