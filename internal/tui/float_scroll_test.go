package tui

import (
	tea "charm.land/bubbletea/v2"

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
		if want := lines - previewVisible(lines, fh); m.previewScroll != want {
			t.Errorf("%d lines: scrolled to %d, want %d (float is %d tall)", lines, m.previewScroll, want, fh)
		}
		m.invalidateFrame()
		screen := ansi.Strip(m.View().Content)
		if last := fmt.Sprintf("line %03d", lines); !strings.Contains(screen, last) {
			t.Errorf("%d lines: %q is not on screen at the end of the scroll", lines, last)
		}
		if !strings.Contains(screen, "100%") {
			t.Errorf("%d lines: no 100%% marker at the end of the scroll", lines)
		}
	}
}

// While the content overflows, the pane's bottom row says how much of it
// has been seen; content that fits shows no marker and keeps every row.
func TestOverflowingPreviewShowsAPercentage(t *testing.T) {
	m := floatedTall(t, 60)
	m.invalidateFrame()
	_, fh := m.floatDims()
	want := fmt.Sprintf("%d%%", previewVisible(60, fh)*100/60)
	if screen := ansi.Strip(m.View().Content); !strings.Contains(screen, want) {
		t.Errorf("an overflowing float shows no %s marker:\n%s", want, screen)
	}
	short := floatedTall(t, 5)
	short.invalidateFrame()
	if screen := ansi.Strip(short.View().Content); strings.Contains(screen, "%") {
		t.Errorf("a float that fits shows a percentage:\n%s", screen)
	}
}

// Under a float the list spans the full width, so the wheel used to go to
// it and move the selection, which closed the float. Over the box it
// scrolls the box.
func TestWheelOverAFloatScrollsIt(t *testing.T) {
	m := floatedTall(t, 60)
	bx, by, bw, bh, _, _ := m.floatBox()
	got, _ := m.wheel(tea.MouseWheelMsg{X: bx + bw/2, Y: by + bh/2, Button: tea.MouseWheelDown})
	m = got.(Model)
	if m.previewScroll == 0 {
		t.Error("the wheel over the float did not scroll it")
	}
	if !m.floating() {
		t.Error("the wheel closed the float")
	}
}
