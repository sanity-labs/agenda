package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Swimlane grouping, shared by every view. Grouping is derived from the
// active sort: a view declares a label function per sort mode (nil = no
// feasible grouping, stay flat), and because the label follows the sort's
// primary key, equal labels are always contiguous, so grouping reduces to
// inserting a header row wherever the label changes. The s/S sort semantics
// stay untouched; 'S' reverses the lanes along with the items.

// GroupingMsg toggles swimlane grouping in every view. Broadcast by the root
// model at startup (from config) and on live config edits.
type GroupingMsg bool

// InsertGroups walks items (already sorted so equal labels are adjacent) and
// inserts a header row built by header wherever the label changes.
func InsertGroups[T any](items []T, label func(T) string, header func(string) T) []T {
	if len(items) == 0 {
		return items
	}
	out := make([]T, 0, len(items)+8)
	last := ""
	for i, it := range items {
		if l := label(it); i == 0 || l != last {
			out = append(out, header(l))
			last = l
		}
		out = append(out, it)
	}
	return out
}

// GroupHeader draws a swimlane header as a two-line block (blank line +
// labeled rule): an accent diamond and bold label over a dim rule. A diamond
// rather than a bar, so it never visually merges with the selection bar of
// the row beneath it; also distinct from SectionSeparator, which marks
// larger sections (the PR view's "needs your review" split).
func GroupHeader(label string, width int) string {
	text := "◆ " + label
	fill := max(0, width-lipgloss.Width(text)-2)
	return "\n" + Accent.Bold(true).Render(text) + " " + Dim.Render(strings.Repeat("─", fill))
}

// timeBuckets are the swimlanes for date-ordered lists, newest first.
var timeBuckets = []struct {
	label  string
	within time.Duration
}{
	{"Last 7 days", 7 * 24 * time.Hour},
	{"Last 30 days", 30 * 24 * time.Hour},
}

// TimeBucketAt buckets a timestamp for time swimlanes, relative to now:
// Today, Yesterday, Last 7 days, Last 30 days, Older. Today and Yesterday
// are calendar days in local time, not 24h windows.
func TimeBucketAt(t, now time.Time) string {
	ny, nm, nd := now.Date()
	today := time.Date(ny, nm, nd, 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today):
		return "Today"
	case !t.Before(today.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	for _, b := range timeBuckets {
		if now.Sub(t) <= b.within {
			return b.label
		}
	}
	return "Older"
}

// TimeBucket is TimeBucketAt against the current time.
func TimeBucket(t time.Time) string { return TimeBucketAt(t, time.Now()) }

// RevealPreviewMsg asks the root model to unhide the preview pane: a view is
// about to render something into it (a diff, comments, a thread jump) and a
// hidden pane would leave the user wondering where it went.
type RevealPreviewMsg struct{}

// RevealPreview is the tea.Cmd form of RevealPreviewMsg.
func RevealPreview() tea.Msg { return RevealPreviewMsg{} }

// ConcealPreviewMsg returns a transiently-revealed preview (see
// RevealPreviewMsg) to its hidden state: the selection moved on, or the
// action that needed the pane finished. A no-op when the user showed the
// pane deliberately.
type ConcealPreviewMsg struct{}

// ConcealPreview is the tea.Cmd form of ConcealPreviewMsg.
func ConcealPreview() tea.Msg { return ConcealPreviewMsg{} }

// TogglesPersistMsg tells every view whether a per-item toggle (a diff pane,
// an expanded description, shown comments) survives moving to another item.
// Broadcast by the root model at startup and when the setting changes.
type TogglesPersistMsg bool

// ListJumpMsg tells every view how many rows the list jump keys (pgup,
// pgdn, shift+arrows) move. Broadcast at startup and on change.
type ListJumpMsg int

// UnreadMsg tells every view whether to draw the marks on rows that arrived
// since the last fetch; the marks are recorded regardless. Broadcast by the
// root model at startup and on change.
type UnreadMsg bool

// PreviewShownMsg tells every view whether the detail pane is on screen.
// Views use it to decide what counts as reading a row: with the detail
// visible, moving onto a row shows it; with it hidden, you have to ask.
type PreviewShownMsg bool

// UnreadSyncMsg tells the PR view whether reading a row should also mark
// the matching GitHub notification read. Off unless asked for: a cursor
// move should not quietly clear your real inbox.
type UnreadSyncMsg bool

// PreviewFloatingMsg tells the views whether the detail on screen is a
// float, which ends when the selection moves, or a pane that stays open
// and so shows the row you arrive at.
type PreviewFloatingMsg bool

// ConfigSetMsg asks the root model to persist a config value a view has
// already applied to its own copy. Views cannot write the file themselves,
// and the root model owns the live config.
type ConfigSetMsg struct {
	Path  string
	Value string
}
