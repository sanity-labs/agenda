package prs

import (
	"regexp"
	"testing"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The dot exists to catch the eye, so it must not share a colour with the
// swimlane header it sits under. Checked in every built-in theme: magenta
// was the obvious pick and is identical to the accent in five of them.
func TestDotColourDiffersFromHeader(t *testing.T) {
	for _, theme := range []string{"default", "catppuccin-mocha", "tokyonight", "gruvbox", "dracula", "nord", "rose-pine", "catppuccin-latte"} {
		p, err := ui.ResolvePalette(theme, nil)
		if err != nil {
			t.Fatal(err)
		}
		ui.SetPalette(p)

		row := pr{Number: 1, URL: "u", Unread: true, UnreadGutter: true}
		row.Repository.NameWithOwner = "o/r"
		dot := firstSGR(row.Render(80, false, ui.Highlighter{}))
		header := firstSGR(ui.GroupHeader("Today", 80))
		if dot == header {
			t.Errorf("%s: unread dot and group header share %q", theme, dot)
		}
	}
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func firstSGR(s string) string {
	for _, m := range sgr.FindAllString(s, -1) {
		if m != "\x1b[m" && m != "\x1b[0m" {
			return m
		}
	}
	return ""
}
