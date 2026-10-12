package ui

import (
	"strings"
	"unicode"
)

// SpinnerFrames is the braille spinner cycled while something is in flight.
// The root model drives the frame; a view animating its own glyph follows
// it through SpinnerTickMsg so every spinner on screen turns together.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerFrame is the glyph for frame i.
func SpinnerFrame(i int) string { return SpinnerFrames[i%len(SpinnerFrames)] }

// SpinnerTickMsg carries the current spinner frame to every view.
type SpinnerTickMsg struct{ Frame int }

// fuzzyIndices are the rune positions in s that match q, or nil. A match is
// q as a contiguous run, or runs that each open a word, every run after the
// first at least two runes long: "linpag" finds "linear paging" and "foo"
// finds "foo" or "fix: oops", but not initials or letters scattered through
// "fetch the old owner". A free subsequence matched nearly every row for a
// three-letter query.
func fuzzyIndices(s, q []rune) []int {
	if len(q) == 0 || len(q) > len(s) {
		return nil
	}
	if i := strings.Index(string(s), string(q)); i >= 0 {
		start := len([]rune(string(s)[:i]))
		idx := make([]int, len(q))
		for j := range idx {
			idx[j] = start + j
		}
		return idx
	}
	idx := make([]int, 0, len(q))
	var walk func(from, qi int) bool
	walk = func(from, qi int) bool {
		if qi == len(q) {
			return true
		}
		for i := from; i <= len(s)-(len(q)-qi); i++ {
			if s[i] != q[qi] {
				continue
			}
			switch {
			case qi > 0 && i == idx[qi-1]+1, qi == 0 && wordStart(s, i):
				idx = append(idx, i)
				if walk(i+1, qi+1) {
					return true
				}
			case wordStart(s, i) && qi+1 < len(q) && s[i+1] == q[qi+1]:
				// A new run opens a word and takes two runes at once.
				idx = append(idx, i, i+1)
				if walk(i+2, qi+2) {
					return true
				}
			default:
				continue
			}
			idx = idx[:qi]
		}
		return false
	}
	if walk(0, 0) {
		return idx
	}
	return nil
}

// wordStart reports whether s[i] opens a word: the first rune, or one after
// a separator, or an upper-case rune after a lower-case one (camelCase).
func wordStart(s []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := s[i-1]
	if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsUpper(s[i]) && unicode.IsLower(prev)
}
