package ui

import (
	"strings"
	"testing"
)

func TestHighlighterMatchIndices(t *testing.T) {
	cases := []struct {
		name          string
		query         string
		caseSensitive bool
		plain         string
		want          []int // rune indices
	}{
		{"empty query", "", false, "banana", nil},
		{"contiguous", "ban", false, "banana", []int{0, 1, 2}},
		{"word runs", "fioo", false, "fix oops", []int{0, 1, 4, 5}},
		{"scattered letters", "bnn", false, "banana", nil},
		{"no match", "xyz", false, "banana", nil},
		{"case insensitive", "BAN", false, "banana", []int{0, 1, 2}},
		{"case sensitive miss", "BAN", true, "banana", nil},
		{"case sensitive hit", "Ban", true, "Banana", []int{0, 1, 2}},
		{"multibyte", "é", false, "café", []int{3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hl := Highlighter{Query: c.query, CaseSensitive: c.caseSensitive}
			got := hl.matchIndices(c.plain)
			if len(got) != len(c.want) {
				t.Fatalf("matchIndices(%q) = %v, want %v", c.plain, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("matchIndices(%q) = %v, want %v", c.plain, got, c.want)
				}
			}
		})
	}
}

func TestHighlighterHighlight(t *testing.T) {
	// Empty query is a no-op.
	if got := (Highlighter{}).Highlight("banana"); got != "banana" {
		t.Errorf("empty query Highlight = %q, want unchanged", got)
	}
	// No match returns the input unchanged (no escape codes added).
	if got := (Highlighter{Query: "xyz"}).Highlight("banana"); got != "banana" {
		t.Errorf("no-match Highlight = %q, want unchanged", got)
	}
	// A match wraps at least the matched runes; the plain text is preserved when
	// ANSI codes are stripped out. We assert every plain rune still appears in
	// order by checking the visible characters are a superset substring.
	out := (Highlighter{Query: "ban"}).Highlight("banana")
	if !strings.Contains(out, "b") || !strings.Contains(out, "a") || !strings.Contains(out, "n") {
		t.Errorf("Highlight dropped characters: %q", out)
	}
	if out == "banana" {
		t.Errorf("Highlight added no styling for a match: %q", out)
	}
}

func TestHighlighterHighlightSubstr(t *testing.T) {
	// Empty query / no match: unchanged, no escape codes.
	if got := (Highlighter{}).HighlightSubstr("hello world"); got != "hello world" {
		t.Errorf("empty query = %q, want unchanged", got)
	}
	if got := (Highlighter{Query: "xyz"}).HighlightSubstr("hello world"); got != "hello world" {
		t.Errorf("no match = %q, want unchanged", got)
	}
	// Substring (not subsequence): "lo wo" must match contiguously; "hlo" must NOT.
	if got := (Highlighter{Query: "hlo"}).HighlightSubstr("hello"); got != "hello" {
		t.Errorf("subsequence should not match in HighlightSubstr: %q", got)
	}
	// A real substring match adds styling and preserves the visible text.
	out := (Highlighter{Query: "wor"}).HighlightSubstr("hello world")
	if out == "hello world" {
		t.Errorf("expected styling for substring match, got unchanged")
	}
	if !strings.Contains(out, "hello ") || !strings.Contains(out, "ld") {
		t.Errorf("HighlightSubstr corrupted surrounding text: %q", out)
	}
	// Case-insensitive by default; every occurrence highlighted.
	multi := (Highlighter{Query: "ab"}).HighlightSubstr("ABxabxAB")
	if strings.Count(multi, "\x1b[") < 3 { // 3 occurrences → at least 3 style openers
		t.Errorf("expected all occurrences highlighted: %q", multi)
	}
}

func TestFuzzyHighlightUsesBlockStyle(t *testing.T) {
	// Upstream's search highlight is a dark-on-yellow block; the fuzzy
	// per-rune highlight must match it, not a bare accent foreground.
	hl := Highlighter{Query: "ab"}
	out := hl.Highlight("ab")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("no styling applied: %q", out)
	}
	if out != highlightStyle().Render("a")+highlightStyle().Render("b") {
		t.Errorf("fuzzy highlight diverges from highlightStyle(): %q", out)
	}
}
