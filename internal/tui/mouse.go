package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Mouse input: the wheel scrolls whichever pane it's over, a left click on the
// tab bar switches views, a click in the list column selects the row under it,
// and a second click on the same row runs the view's primary action.

// doubleClickWindow is how soon a second click on the same row must follow
// the first to count as a double-click; bubbletea reports single clicks only.
const doubleClickWindow = 400 * time.Millisecond

// scroller is implemented by views whose list can be scrolled by the mouse
// wheel. The command is whatever the view runs when its selection moves.
type scroller interface {
	ScrollList(n int) tea.Cmd
}

// clicker is implemented by views that react to a left click in the list
// column. x and y are relative to the column's top-left cell (just below the
// tab bar). row reports whether the click landed on a selectable row, which
// makes it eligible for a double-click.
type clicker interface {
	ClickList(x, y int) (row bool, cmd tea.Cmd)
}

// activator is implemented by views whose selection has a primary action
// (what enter does), run on a double-click.
type activator interface {
	Activate() tea.Cmd
}

// clickRecord is the last row click: the view, its selection, and when.
type clickRecord struct {
	view int
	key  string
	at   time.Time
}

// A wheel notch reaches us as a burst of wheel events (three, in Ghostty),
// written together, while a trackpad swipe spreads its events over time.
// bubbletea draws a frame after every message, so a notch's later events are
// already queued when the frame after its first one finishes: one that arrives
// within wheelQueued of a frame, following a same-direction event within
// wheelBurstSpan, is part of the same notch. The span must outlast the
// slowest frame, since a new selection can take tens of milliseconds to draw.
const (
	wheelQueued    = 3 * time.Millisecond
	wheelBurstSpan = 250 * time.Millisecond
)

// wheelState is the wheel history the burst check needs. It sits behind a
// pointer so View, which has a value receiver, can record when it finished.
type wheelState struct {
	drawnAt time.Time // when View last returned
	at      time.Time // the previous wheel event over the list
	dir     int
}

// wheel scrolls the pane under the pointer. The preview moves a line per
// event, so a notch scrolls as many lines as the terminal sends events for it
// (three, in Ghostty) and a trackpad swipe stays smooth. The list moves a row
// per notch: a jump of several rows loses your place, so the rest of a
// notch's burst is dropped there.
func (m Model) wheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if !m.ready || len(m.views) == 0 || m.modalOpen() {
		return m, nil
	}
	var dir int
	switch msg.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	default:
		return m, nil // ignore horizontal wheel
	}
	listW, _, _ := m.dims()
	if msg.X >= listW {
		m.scrollPreview(dir)
		return m, nil
	}
	if m.wheelBurst(dir) {
		return m, nil
	}
	return m, m.scrollList(dir)
}

// wheelBurst records a wheel event over the list and reports whether it is
// the tail of a notch whose first event already scrolled.
func (m Model) wheelBurst(dir int) bool {
	w := m.wheelSt
	if w == nil {
		return false
	}
	now := time.Now()
	burst := w.dir == dir && now.Sub(w.at) < wheelBurstSpan && now.Sub(w.drawnAt) < wheelQueued
	w.at, w.dir = now, dir
	return burst
}

// scrollList forwards a wheel scroll to the focused view's list, if it supports it.
func (m *Model) scrollList(n int) tea.Cmd {
	s, ok := m.views[m.current].(scroller)
	if !ok {
		return nil
	}
	cmd := s.ScrollList(n)
	m.syncPreviewKey(false) // scrolling may move the selection
	return cmd
}

// click routes a left click. An open modal takes it first, in the order keys
// reach them: the help overlay closes on any click, the picker and filter
// modal close on a click outside their box, and the config overlays and a
// view's own modal ignore clicks so a stray one can't throw away a half-typed
// edit. So does a view capturing text input. Otherwise the tab bar switches
// views and the list column selects rows.
func (m Model) click(x, y int) (tea.Model, tea.Cmd) {
	switch {
	case m.helpOpen:
		m.helpOpen = false
		return m, nil
	case m.keysEd != nil, m.settings != nil:
		return m, nil
	case m.picker != nil:
		if !m.inCenteredBox(m.picker.View(), x, y) {
			m.picker, m.pickerRefs = nil, nil
		}
		return m, nil
	case m.filter != nil:
		if !m.inCenteredBox(m.filter.View(), x, y) {
			m.filter = nil
		}
		return m, nil
	}
	if len(m.views) == 0 || m.modalOpen() || m.views[m.current].InputActive() {
		return m, nil
	}

	if y < tabBarHeight {
		if i := m.tabAt(x); i >= 0 && i != m.current {
			m.current = i
			m.syncPreviewKey(true)
			m.concealTransient()
			m.lastClick = clickRecord{}
		}
		return m, nil
	}

	listW, _, contentH := m.dims()
	y -= tabBarHeight
	c, ok := m.views[m.current].(clicker)
	if !ok || x >= listW || y >= contentH {
		return m, nil // preview, footer, or a view without a clickable list
	}
	row, cmd := c.ClickList(x, y)
	m.syncPreviewKey(false)
	if !row {
		m.lastClick = clickRecord{}
		return m, cmd
	}

	// Identify the row by the selection it produced, so a list that shifted
	// between the two clicks can't turn them into a double-click on
	// different rows.
	now := time.Now()
	hit := clickRecord{view: m.current, key: m.views[m.current].PreviewKey(), at: now}
	prev := m.lastClick
	if prev.view == hit.view && prev.key == hit.key && now.Sub(prev.at) <= doubleClickWindow {
		m.lastClick = clickRecord{} // a third click starts over
		if a, ok := m.views[m.current].(activator); ok {
			return m, tea.Batch(cmd, a.Activate())
		}
		return m, cmd
	}
	m.lastClick = hit
	return m, cmd
}

// modalOpen reports whether an overlay is capturing input, so the mouse
// mustn't reach the panes underneath.
func (m Model) modalOpen() bool {
	if m.helpOpen || m.keysEd != nil || m.settings != nil || m.picker != nil || m.filter != nil {
		return true
	}
	if len(m.views) == 0 {
		return false
	}
	o, ok := m.views[m.current].(overlayProvider)
	return ok && o.Overlay() != ""
}

// tabAt returns the index of the tab whose label spans column x, or -1. The
// labels sit side by side from column 0 (see renderTabs).
func (m Model) tabAt(x int) int {
	left := 0
	for i, label := range m.tabLabels() {
		w := lipgloss.Width(label)
		if x >= left && x < left+w {
			return i
		}
		left += w
	}
	return -1
}

// inCenteredBox reports whether (x, y) falls inside box as View composites it.
func (m Model) inCenteredBox(box string, x, y int) bool {
	bx, by := m.centerOf(box)
	return x >= bx && x < bx+lipgloss.Width(box) && y >= by && y < by+lipgloss.Height(box)
}
