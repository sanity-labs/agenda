package ui

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// allIcons is every glyph constant, so a malformed escape in any of them
// fails here rather than rendering as rubbish in a row.
var allIcons = map[string]string{
	"IconOpen": IconOpen, "IconDraft": IconDraft, "IconMerged": IconMerged,
	"IconClosed": IconClosed, "IconCIOK": IconCIOK, "IconCIFail": IconCIFail,
	"IconCIPending": IconCIPending, "IconApproved": IconApproved,
	"IconChanges": IconChanges, "IconReviewReq": IconReviewReq,
	"IconComment": IconComment, "IconDot": IconDot,
	"IconSection": IconSection, "IconUnread": IconUnread,
	"IconPillLeft": IconPillLeft, "IconPillRight": IconPillRight,
	"IconTabPRs": IconTabPRs, "IconTabSessions": IconTabSessions,
	"IconTabLinear": IconTabLinear, "IconNavMine": IconNavMine,
	"IconNavInbox": IconNavInbox, "IconNavAll": IconNavAll,
	"IconNavProject": IconNavProject, "IconStar": IconStar,
	"IconBell":        IconBell,
	"IconAgentClaude": IconAgentClaude, "IconAgentCodex": IconAgentCodex,
	"IconAgentAgy": IconAgentAgy,
}

// Every icon is exactly one printable rune. This catches the mistake Go
// cannot: "\f111" is a legal string (form feed then "111"), so it builds
// and renders as a control character plus three digits. \u needs 4 hex
// digits and \U needs 8, and getting that wrong produces text, not an
// error.
func TestEveryIconIsOnePrintableRune(t *testing.T) {
	for name, icon := range allIcons {
		t.Run(name, func(t *testing.T) {
			if icon == "" {
				t.Fatal("empty: a glyph was stripped, or the escape is missing")
			}
			runes := []rune(icon)
			if len(runes) != 1 {
				t.Fatalf("%q is %d runes (%U), want exactly 1: a malformed"+
					" escape leaves the remaining digits as text",
					icon, len(runes), runes)
			}
			// Not IsPrint: every Nerd Font glyph is Private Use Area, which
			// IsPrint rejects. A control character is the actual failure.
			r := runes[0]
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				t.Errorf("%U is a control or space character; \\f, \\v and"+
					" friends are legal escapes but not glyphs", r)
			}
			if unicode.IsDigit(r) || unicode.IsLetter(r) {
				t.Errorf("%U is a letter or digit (%q); an icon should be a"+
					" symbol", r, icon)
			}
		})
	}
}

// An icon's trailing comment shows the glyph it renders, so a reader can
// see it without decoding the escape. A comment that disagrees with the
// escape is worse than none: it says the row shows something it does not.
func TestIconCommentsMatchTheirEscapes(t *testing.T) {
	src := readIconSources(t)
	for _, line := range strings.Split(src, "\n") {
		name, icon, comment, ok := parseIconLine(line)
		if !ok {
			continue
		}
		want, exists := allIcons[name]
		if !exists || comment == "" {
			continue
		}
		// Some entries document the codepoint as text ("U+EE0D") instead of
		// showing the glyph. Both are fine; only a glyph can disagree.
		if len(comment) != len([]rune(comment)) || len([]rune(comment)) != 1 {
			continue
		}
		if icon != want {
			continue // covered by the test above
		}
		if comment != want {
			t.Errorf("%s renders %U but its comment shows %U; one of them is wrong",
				name, []rune(want), []rune(comment))
		}
	}
}

// readIconSources concatenates the files that declare glyph constants.
func readIconSources(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, f := range []string{"icons.go", "agenticons.go"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// parseIconLine pulls the constant name, its value and its trailing
// comment glyph out of a declaration line.
func parseIconLine(line string) (name, icon, comment string, ok bool) {
	m := iconLineRE.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", false
	}
	val, err := strconv.Unquote(`"` + m[2] + `"`)
	if err != nil {
		return "", "", "", false
	}
	return m[1], val, strings.TrimSpace(m[3]), true
}

// name = "value" // comment
var iconLineRE = regexp.MustCompile(`^\s*(Icon\w+)\s*=\s*"([^"]*)"\s*(?://\s*(.*))?$`)
