package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// floatBox has to agree with where the box is actually rendered, or clicks
// land on the list underneath: while floating the list spans the full
// width, so a misrouted click moves the selection and shuts the window.
func TestFloatBoxMatchesTheRenderedBox(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	for _, size := range []struct{ w, h int }{{100, 40}, {120, 50}, {60, 24}} {
		m := New(cfg, []View{&fatView{title: "PRs"}})
		m.width, m.height, m.ready = size.w, size.h, true
		m.layout()
		got, _ := m.Update(ui.RevealPreviewMsg{})
		m = got.(Model)
		m.invalidateFrame()

		bx, by, bw, bh, _, _ := m.floatBox()
		lines := strings.Split(m.View().Content, "\n")

		// The top border row must be where floatBox says, and start there.
		if by >= len(lines) {
			t.Fatalf("%dx%d: floatBox y=%d beyond the screen (%d lines)",
				size.w, size.h, by, len(lines))
		}
		top := lines[by]
		if col := lipgloss.Width(stripAfter(top, "╭")); col != bx {
			t.Errorf("%dx%d: box starts at column %d, floatBox says %d",
				size.w, size.h, col, bx)
		}
		if w := boxWidth(top); w != bw {
			t.Errorf("%dx%d: rendered box is %d wide, floatBox says %d",
				size.w, size.h, w, bw)
		}
		// The bottom border row must be the last row of the box.
		if bottom := by + bh - 1; bottom >= len(lines) ||
			!strings.Contains(lines[bottom], "╰") {
			t.Errorf("%dx%d: no bottom border at row %d (h=%d)",
				size.w, size.h, bottom, bh)
		}
	}
}

// A click inside the box reaches the view; one outside dismisses it. The
// bug was that neither happened: every click moved the selection.
func TestClickInsideFloatReachesTheViewAndOutsideDismisses(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	v := &clickSpyView{fatView: fatView{title: "PRs"}}
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	if !m.floating() {
		t.Fatal("setup: not floating")
	}

	bx, by, bw, bh, _, _ := m.floatBox()
	got, _ = m.click(bx+bw/2, by+bh/2)
	m = got.(Model)
	if v.previewClicks == 0 {
		t.Error("a click inside the float did not reach the view")
	}
	if v.listClicks > 0 {
		t.Error("a click inside the float also hit the list underneath")
	}
	if !m.floating() {
		t.Error("a click inside the float closed it")
	}

	// Outside the box: dismissed, and the list is not clicked either.
	v.listClicks, v.previewClicks = 0, 0
	got, _ = m.click(1, by+bh/2)
	m = got.(Model)
	if m.floating() {
		t.Error("a click outside the float left it open")
	}
	if v.listClicks > 0 {
		t.Error("the dismissing click also moved the selection")
	}
}

// clickSpyView counts where clicks were routed.
type clickSpyView struct {
	fatView
	listClicks, previewClicks int
}

func (s *clickSpyView) ClickList(x, y int) (bool, tea.Cmd) {
	s.listClicks++
	return true, nil
}

func (s *clickSpyView) ClickPreview(line, col int) tea.Cmd {
	s.previewClicks++
	return nil
}

// stripAfter returns the text before the first occurrence of sep.
func stripAfter(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i]
	}
	return s
}

// boxWidth measures the border run in a rendered top border row.
func boxWidth(line string) int {
	start := strings.Index(line, "╭")
	end := strings.Index(line, "╮")
	if start < 0 || end < 0 {
		return -1
	}
	return lipgloss.Width(line[start : end+len("╮")])
}
