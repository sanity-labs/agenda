package linear

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The dot has to render, and the row must be the same width either way, or
// clearing a mark shifts the list.
func TestFreshRendersAndKeepsAlignment(t *testing.T) {
	marked := issue{Identifier: "SRE-1", Title: "a thing", Fresh: true, FreshGutter: true}
	read := marked
	read.Fresh = false

	got := marked.Render(80, false, ui.Highlighter{})
	if !strings.Contains(got, ui.IconUnread) {
		t.Errorf("fresh issue has no mark:\n%s", got)
	}
	width := func(s string) int { return lipgloss.Width(strings.Split(s, "\n")[0]) }
	if a, b := width(got), width(read.Render(80, false, ui.Highlighter{})); a != b {
		t.Errorf("row widths differ: fresh %d, read %d", a, b)
	}
}

// Turning the feature off drops existing marks rather than freezing them.
func TestUnreadMsgClearsFresh(t *testing.T) {
	v := &View{fresh: map[string]bool{"SRE-1": true}, unreadOn: true}
	v.list = ui.NewList[issue]()
	v.Update(ui.UnreadMsg(false))
	if len(v.fresh) != 0 {
		t.Errorf("fresh = %v after turning unread off, want cleared", v.fresh)
	}
}
