package ui

import "testing"

// The approved and draft marks are specific glyphs the user picked; a
// silent change to either is a change to how every PR row reads.
func TestReviewStateIconCodepoints(t *testing.T) {
	for _, c := range []struct {
		name string
		got  string
		want rune
	}{
		{"approved", IconApproved, 0xedc6},
		{"draft", IconDraft, 0xebdb},
	} {
		runes := []rune(c.got)
		if len(runes) != 1 || runes[0] != c.want {
			t.Errorf("the %s icon = %q (%U), want %U",
				c.name, c.got, runes, c.want)
		}
	}
}
