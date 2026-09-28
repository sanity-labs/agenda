package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

// bigView stands in for a PR with a long description: PreviewView is
// expensive and the body is large, which is when the stall shows up.
type bigView struct {
	stubView
	body  string
	calls int
}

func (b *bigView) PreviewView() string {
	b.calls++
	// Mimic the real cost: build the string fresh each call.
	var sb strings.Builder
	sb.Grow(len(b.body))
	sb.WriteString(b.body)
	return sb.String()
}

func newBigModel() (Model, *bigView) {
	v := &bigView{stubView: stubView{"PRs"}}
	v.body = strings.Repeat("a line of rendered markdown preview text\n", 2000)
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 120, 40, true
	return m, v
}

// A wheel event over the preview must not re-render the preview just to
// measure it: that doubles the per-event cost and stalls a fast scroll.
func TestWheelDoesNotReRenderPreview(t *testing.T) {
	m, v := newBigModel()
	listW, _, _ := m.dims()
	v.calls = 0
	for i := 0; i < 50; i++ {
		got, _ := m.wheel(tea.MouseWheelMsg{X: listW + 1, Y: 5, Button: tea.MouseWheelDown})
		m = got.(Model)
	}
	if v.calls > 1 {
		t.Errorf("PreviewView called %d times for 50 wheel events, want at most 1 (cached)", v.calls)
	}
}

func BenchmarkWheelOverPreview(b *testing.B) {
	m, _ := newBigModel()
	listW, _, _ := m.dims()
	msg := tea.MouseWheelMsg{X: listW + 1, Y: 5, Button: tea.MouseWheelDown}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, _ := m.wheel(msg)
		m = got.(Model)
	}
}

// loadingView mimics a pane whose content arrives asynchronously: the preview
// key is stable, but the text changes once data lands. A cache must not pin
// the "Loading…" frame on screen.
type loadingView struct {
	stubView
	loaded bool
}

func (l *loadingView) PreviewView() string {
	if !l.loaded {
		return "Loading diff…"
	}
	return "diff line one\ndiff line two\ndiff line three"
}

func TestPreviewCacheRefreshesWhenDataLands(t *testing.T) {
	v := &loadingView{stubView: stubView{"PRs"}}
	m := New(config.Default(), []View{v})
	m.width, m.height, m.ready = 120, 40, true

	if text, _ := m.renderedPreview(v); !strings.Contains(text, "Loading") {
		t.Fatalf("first render = %q, want the loading frame", text)
	}
	v.loaded = true
	m.previewInvalidate() // what the data-arrival path must do
	text, lines := m.renderedPreview(v)
	if strings.Contains(text, "Loading") {
		t.Error("cache pinned the loading frame after data arrived")
	}
	if lines != 3 {
		t.Errorf("lines = %d, want 3", lines)
	}
}

// A free-spinning wheel floods events faster than frames can drain them. The
// scroll must cover more ground per event so it keeps up, and must drop back
// to one line per event the moment the wheel stops.
func TestSpinningWheelAccelerates(t *testing.T) {
	m, _ := newBigModel()
	listW, _, _ := m.dims()
	down := tea.MouseWheelMsg{X: listW + 1, Y: 5, Button: tea.MouseWheelDown}

	// A steady spin: many events with no gap.
	for i := 0; i < 20; i++ {
		got, _ := m.wheel(down)
		m = got.(Model)
	}
	spun := m.previewScroll
	if spun <= 20 {
		t.Errorf("20 spun events scrolled %d lines, want more than one per event", spun)
	}

	// The wheel stops: the next event after a pause is a single line again.
	m.wheelSt.prevAt = time.Now().Add(-time.Second)
	m.wheelSt.fast = 0
	before := m.previewScroll
	got, _ := m.wheel(down)
	m = got.(Model)
	if d := m.previewScroll - before; d != 1 {
		t.Errorf("after the wheel stopped, one event scrolled %d lines, want 1", d)
	}
}

// Spinning the wheel past the end must not cost a frame each: the offset
// cannot move, so the rendered view is identical and should be reused.
func TestScrollAtBoundaryReusesFrame(t *testing.T) {
	m, v := newBigModel()
	listW, _, _ := m.dims()
	down := tea.MouseWheelMsg{X: listW + 1, Y: 5, Button: tea.MouseWheelDown}

	// Drive to the bottom.
	for i := 0; i < 4000; i++ {
		got, _ := m.wheel(down)
		m = got.(Model)
	}
	_ = m.View()
	atEnd := m.previewScroll

	v.calls = 0
	frames := 0
	for i := 0; i < 50; i++ {
		got, _ := m.wheel(down)
		m = got.(Model)
		if m.frameDirty() {
			frames++
		}
		_ = m.View()
	}
	if m.previewScroll != atEnd {
		t.Fatalf("offset moved past the end: %d -> %d", atEnd, m.previewScroll)
	}
	if frames != 0 {
		t.Errorf("%d frames redrawn while pinned at the end, want 0", frames)
	}
	if v.calls != 0 {
		t.Errorf("PreviewView called %d times while pinned, want 0", v.calls)
	}
}

// The frame cache must never outlive a real change: scrolling back from the
// boundary, moving the selection, and async data all have to redraw.
func TestFrameCacheDoesNotGoStale(t *testing.T) {
	m, _ := newBigModel()
	listW, _, _ := m.dims()
	down := tea.MouseWheelMsg{X: listW + 1, Y: 5, Button: tea.MouseWheelDown}
	up := tea.MouseWheelMsg{X: listW + 1, Y: 5, Button: tea.MouseWheelUp}

	for i := 0; i < 4000; i++ { // pin to the bottom
		got, _ := m.Update(down)
		m = got.(Model)
		_ = m.View()
	}
	got, _ := m.Update(down) // no-op: frame reused
	m = got.(Model)
	if m.frameDirty() {
		t.Error("frame invalidated by a no-op scroll, want it reused")
	}

	got, _ = m.Update(up) // real movement: must redraw
	m = got.(Model)
	if !m.frameDirty() {
		t.Error("frame reused after scrolling back from the end, want a redraw")
	}
	_ = m.View()

	// Any other message must redraw too.
	got, _ = m.Update(tea.KeyPressMsg{Code: 'j'})
	m = got.(Model)
	if !m.frameDirty() {
		t.Error("frame reused after a key press, want a redraw")
	}
}
