package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Highlighter carries the active filter query for in-row highlighting of the
// runes that participate in a subsequence match. The zero value (empty Query)
// highlights nothing.
type Highlighter struct {
	Query         string
	CaseSensitive bool
}

// highlightStyle mimics Vim's `Search` highlight: dark text on a yellow
// background, so matches stand out as a solid block rather than just colored
// glyphs. Computed per call so the yellow follows the active palette.
func highlightStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color(Pal().Yellow))
}

// matchIndices returns the rune indices of plain (indexing into []rune(plain))
// that match Query, in order: the same positions the list filter accepted.
// nil if Query is empty or there is no match.
func (hl Highlighter) matchIndices(plain string) []int {
	if hl.Query == "" {
		return nil
	}
	s := []rune(plain)
	q := []rune(hl.Query)
	if !hl.CaseSensitive {
		s = []rune(strings.ToLower(plain))
		q = []rune(strings.ToLower(hl.Query))
	}
	return fuzzyIndices(s, q)
}

// HighlightSubstr wraps every literal (contiguous) occurrence of Query in plain
// with the highlight style — Vim-style search highlighting. Unlike Highlight
// (fuzzy subsequence, for compact row fields), this is for long prose like the
// preview pane, where scattering single-rune highlights would be noise. Returns
// plain unchanged when Query is empty or absent.
func (hl Highlighter) HighlightSubstr(plain string) string {
	q := strings.TrimSpace(hl.Query)
	if q == "" {
		return plain
	}
	hay, needle := plain, q
	if !hl.CaseSensitive {
		hay, needle = strings.ToLower(plain), strings.ToLower(q)
	}
	if !strings.Contains(hay, needle) {
		return plain
	}
	var b strings.Builder
	for {
		i := strings.Index(hay, needle)
		if i < 0 {
			b.WriteString(plain)
			break
		}
		b.WriteString(plain[:i])
		b.WriteString(highlightStyle().Render(plain[i : i+len(needle)]))
		plain = plain[i+len(needle):]
		hay = hay[i+len(needle):]
	}
	return b.String()
}

// Highlight returns plain with its matched runes wrapped in the highlight
// style. It styles each matched rune individually (the match may be
// non-contiguous, since matching is a fuzzy subsequence). Returns plain
// unchanged when Query is empty or there is no match.
func (hl Highlighter) Highlight(plain string) string {
	idx := hl.matchIndices(plain)
	if len(idx) == 0 {
		return plain
	}
	hit := make(map[int]bool, len(idx))
	for _, i := range idx {
		hit[i] = true
	}
	highlight := highlightStyle()
	var b strings.Builder
	for i, r := range []rune(plain) {
		if hit[i] {
			b.WriteString(highlight.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
