package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTwoLineRowTruncatedMetaKeepsColorsAndWidth(t *testing.T) {
	styled := Cyan.Render("owner/repo") + Yellow.Render(" #42") + Dim.Render(" · a-very-long-branch-name-that-overflows")
	plain := "owner/repo #42 · a-very-long-branch-name-that-overflows"
	out := TwoLineRow(40, false, "●", plain, styled, "1d", "title", Highlighter{})
	line1 := strings.Split(out, "\n")[0]
	if w := lipgloss.Width(line1); w > 40 {
		t.Errorf("line1 width = %d, want <= 40", w)
	}
	if !strings.Contains(line1, "…") {
		t.Error("overflowing meta not truncated with ellipsis")
	}
	if !strings.Contains(line1, "\x1b[") {
		t.Error("truncated meta lost its styling entirely")
	}
}

// A style nested in a band's label ends with a reset, which would close
// the band and leave the rest of the row unfilled: a half-painted band
// reads as a rendering fault, not a section header.
func TestSectionSeparatorSurvivesNestedStyles(t *testing.T) {
	plain := SectionSeparator("LABEL", 60)
	nested := SectionSeparator("LABEL  "+Faint.Italic(true).Render("query"), 60)

	for name, got := range map[string]string{"plain": plain, "nested": nested} {
		if w := lipgloss.Width(got); w != 60 {
			t.Errorf("%s band is %d wide, want 60", name, w)
		}
	}
	// The band's own opening sequence has to appear again after the nested
	// reset, or everything past it renders unstyled.
	open := bandOpen()
	if open == "" {
		t.Fatal("the band emits no opening sequence to restore")
	}
	if strings.Count(nested, open) < 2 {
		t.Errorf("the band is not re-opened after the nested reset:\n%q", nested)
	}
}
