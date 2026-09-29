package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// fatView fills whatever size it is given, so a clipping bug shows up as
// content spilling past the screen rather than as a short page.
type fatView struct {
	title                string
	listW, prevW, height int
}

func (s *fatView) Title() string          { return s.title }
func (s *fatView) Init() tea.Cmd          { return nil }
func (s *fatView) Update(tea.Msg) tea.Cmd { return nil }
func (s *fatView) SetSize(listW, prevW, h int) {
	s.listW, s.prevW, s.height = listW, prevW, h
}

func fill(ch string, w, h int) string {
	rows := make([]string, max(1, h))
	for i := range rows {
		rows[i] = strings.Repeat(ch, max(1, w))
	}
	return strings.Join(rows, "\n")
}

func (s *fatView) ListView() string        { return fill("L", s.listW, s.height+20) }
func (s *fatView) PreviewView() string     { return fill("P", s.prevW, s.height+20) }
func (s *fatView) Bindings() []key.Binding { return nil }
func (s *fatView) Status() string          { return "" }
func (s *fatView) InputActive() bool       { return false }
func (s *fatView) PreviewKey() string      { return "k" }
func (s *fatView) Loading() bool           { return false }

// The float must fit the screen it is drawn on. A style's Height is a
// minimum, so tall content grew the box until it pushed the footer off.
func TestFloatNeverOverflowsTheScreen(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	for _, size := range []struct{ w, h int }{{100, 40}, {60, 20}, {40, 12}, {200, 60}} {
		m := New(cfg, []View{&fatView{title: "PRs"}})
		m.width, m.height, m.ready = size.w, size.h, true
		m.layout()
		got, _ := m.Update(ui.RevealPreviewMsg{})
		m = got.(Model)
		m.invalidateFrame()

		lines := strings.Split(m.View().Content, "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d: rendered %d lines, want at most %d",
				size.w, size.h, len(lines), size.h)
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > size.w {
				t.Errorf("%dx%d: line %d is %d wide, want at most %d",
					size.w, size.h, i, w, size.w)
			}
		}
	}
}

// The float is bordered, so it reads as a window over the list rather than
// blending into it.
func TestFloatIsBordered(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	m := New(cfg, []View{&fatView{title: "PRs"}})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	m.invalidateFrame()

	out := m.View().Content
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if !strings.Contains(out, corner) {
			t.Errorf("float is missing the %q corner; is it bordered?", corner)
		}
	}
	if !strings.Contains(out, "P") {
		t.Error("the float rendered no preview content")
	}
	if !strings.Contains(out, "L") {
		t.Error("the list is not visible around the float")
	}
}
