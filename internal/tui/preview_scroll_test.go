package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

// scrollView asks for preview scrolls the way the PR view's log does.
type scrollView struct {
	fatView
	delta, jump int
}

func (s *scrollView) Update(tea.Msg) tea.Cmd { return nil }

func (s *scrollView) TakePreviewScroll() (int, bool) {
	d := s.delta
	s.delta = 0
	return d, d != 0
}

func (s *scrollView) TakePreviewJump() (int, bool) {
	l := s.jump
	s.jump = 0
	return l, l != 0
}

// A relative request moves from wherever the preview is, so a wheel scroll
// in between is kept rather than undone.
func TestViewScrollIsRelativeToTheModel(t *testing.T) {
	v := &scrollView{fatView: fatView{title: "PRs"}}
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 100, 30, true
	m.layout()
	m.syncPreviewKey(true)
	m.previewScroll = 5 // the wheel moved it

	v.delta = 3
	got, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = got.(Model)
	if m.previewScroll != 8 {
		t.Errorf("previewScroll = %d, want 5+3", m.previewScroll)
	}
}

// A jump requested while handling a data message lands too, not only one
// asked for on a key press.
func TestJumpAppliesAfterABroadcast(t *testing.T) {
	v := &scrollView{fatView: fatView{title: "PRs"}}
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 100, 30, true
	m.layout()

	v.jump = 11
	type dataMsg struct{}
	got, _ := m.Update(dataMsg{})
	m = got.(Model)
	if m.previewScroll != 10 {
		t.Errorf("previewScroll = %d, want the jump's line-1", m.previewScroll)
	}
}

// focusView is a view whose right pane can take the keys.
type focusView struct {
	fatView
	focus string
}

func (f *focusView) PreviewFocus() string { return f.focus }
func (f *focusView) ListView() string     { return "\x1b[31mred row\x1b[0m" }

// With the keys in the preview the list goes grey, the footer names the pane
// that is listening, and the list's own hints step aside.
func TestPreviewFocusDimsListAndLabelsFooter(t *testing.T) {
	v := &focusView{fatView: fatView{title: "PRs"}}
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 160, 30, true
	m.layout()

	footer := m.renderFooter()
	if strings.Contains(footer, "JOBS") || !strings.Contains(footer, "filter") {
		t.Fatalf("unfocused footer should be the usual one: %q", footer)
	}
	m.invalidateFrame()
	if !strings.Contains(m.View().Content, "\x1b[31mred row") {
		t.Fatal("unfocused list should keep its colours")
	}

	v.focus = "jobs"
	footer = m.renderFooter()
	if !strings.Contains(footer, "JOBS") || strings.Contains(footer, "filter") {
		t.Errorf("focused footer should name the pane and drop filter: %q", footer)
	}
	m.invalidateFrame()
	if out := m.View().Content; strings.Contains(out, "\x1b[31m") || !strings.Contains(out, "red row") {
		t.Error("focused list should be greyed out, its own colours stripped")
	}
}

// keeperView keeps its second line lit, as the PR view does its selected row.
type keeperView struct{ focusView }

func (k *keeperView) ListView() string {
	return "\x1b[31mheader\x1b[0m\n\x1b[32mthe PR\x1b[0m\n\x1b[33mother\x1b[0m"
}
func (k *keeperView) FocusKeepLines() (int, int, bool) { return 1, 1, true }

// Only the row the pane is about keeps its colours; the rest of the list goes
// grey.
func TestPreviewFocusKeepsTheSelectedRowLit(t *testing.T) {
	v := &keeperView{focusView{fatView: fatView{title: "PRs"}, focus: "jobs"}}
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 160, 30, true
	m.layout()
	m.invalidateFrame()
	out := m.View().Content
	if !strings.Contains(out, "\x1b[32mthe PR") {
		t.Error("the selected row should keep its colours")
	}
	if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\x1b[33m") {
		t.Error("every other line should be greyed out")
	}
}
