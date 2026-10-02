package ui

import (
	"strings"
	"testing"
)

type blurRow struct{ name string }

func (b blurRow) Render(width int, selected bool, hl Highlighter) string {
	if selected {
		return Accent.Render("> " + b.name)
	}
	return Text.Render("  " + b.name)
}
func (b blurRow) Fields() []Field { return []Field{{Name: "name", Text: b.name}} }
func (b blurRow) Filter() string  { return b.name }

// Blurred, no row reads as selected and the rows dim: the lit cursor in
// the focused pane has to be the only one on screen, or neither says
// which the arrow keys will move.
func TestBlurredListDropsItsCursor(t *testing.T) {
	l := NewList[blurRow]()
	l.SetSize(40, 10)
	l.SetItems([]blurRow{{"one"}, {"two"}, {"three"}})

	focused := l.View()
	if !strings.Contains(focused, ">") {
		t.Fatalf("a focused list shows no cursor:\n%s", focused)
	}

	l.SetBlurred(true)
	blurred := l.View()
	if strings.Contains(blurred, ">") {
		t.Errorf("a blurred list still shows its cursor:\n%s", blurred)
	}
	if !l.Blurred() {
		t.Error("Blurred() does not report the state")
	}
	// Every row carries the faint sequence, so the whole pane recedes.
	for _, line := range strings.Split(blurred, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(line, "\x1b[2m") {
			t.Errorf("a blurred row is not dimmed: %q", line)
		}
	}

	// And it comes back.
	l.SetBlurred(false)
	if !strings.Contains(l.View(), ">") {
		t.Error("unblurring did not restore the cursor")
	}
}

// The cursor still moves while blurred, so returning focus lands where it
// was rather than resetting to the top.
func TestBlurredListKeepsItsPosition(t *testing.T) {
	l := NewList[blurRow]()
	l.SetSize(40, 10)
	l.SetItems([]blurRow{{"one"}, {"two"}, {"three"}})
	l.SetBlurred(true)
	before := l.Selected().name
	l.SetBlurred(false)
	if got := l.Selected().name; got != before {
		t.Errorf("selection moved from %q to %q across a blur", before, got)
	}
}
