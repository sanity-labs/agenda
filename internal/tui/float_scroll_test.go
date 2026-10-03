package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// tallView renders a numbered preview so a test can see which lines made
// it onto the screen.
type tallView struct {
	fatView
	lines int
}

func (v *tallView) PreviewView() string {
	out := make([]string, v.lines)
	for i := range out {
		out[i] = fmt.Sprintf("line %03d", i+1)
	}
	return strings.Join(out, "\n")
}

func floatedTall(t *testing.T, lines int) Model {
	t.Helper()
	cfg := config.Default()
	cfg.HidePreview = true
	m := New(cfg, []View{&tallView{fatView: fatView{title: "PRs"}, lines: lines}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	if !m.floating() {
		t.Fatal("setup: the preview did not float")
	}
	return m
}

// A float is shorter than the pane, so its scroll limit has to come from
// the float's own height: clamping with the pane height left the last
// lines unreachable and the bar short of the bottom.
func TestFloatScrollsToTheLastLine(t *testing.T) {
	_, fh := floatedTall(t, 10).floatDims()
	for _, lines := range []int{fh + 3, 60} { // longer than the float but shorter than the pane; longer than both
		m := floatedTall(t, lines)
		m.scrollPreview(lines) // clamps to the end
		if want := lines - fh; m.previewScroll != want {
			t.Errorf("%d lines: scrolled to %d, want %d (float is %d tall)", lines, m.previewScroll, want, fh)
		}
		m.invalidateFrame()
		if last := fmt.Sprintf("line %03d", lines); !strings.Contains(ansi.Strip(m.View().Content), last) {
			t.Errorf("%d lines: %q is not on screen at the end of the scroll", lines, last)
		}
	}
}
