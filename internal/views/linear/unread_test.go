package linear

import (
	tea "charm.land/bubbletea/v2"

	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
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

// A mark you never looked at has to outlive the process, or quitting
// silently marks everything read (the PR-side bug, in the issues list).
func TestFreshSurvivesRestart(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.LinearConfig{Token: "test-token"}

	first := New(cfg, nil, nil, nil)
	first.SetSize(80, 60, 40)
	first.Update(ui.UnreadMsg(true))
	first.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1"}}, source: first.defaultSource})
	first.Update(loadedMsg{issues: []issue{
		{Identifier: "SRE-1"}, {Identifier: "SRE-2"},
	}, source: first.defaultSource})
	if !first.fresh["SRE-2"] {
		t.Fatalf("nothing marked before the restart: fresh = %v", first.fresh)
	}

	second := New(cfg, nil, nil, nil)
	second.SetSize(80, 60, 40)
	second.Update(ui.UnreadMsg(true))
	if !second.fresh["SRE-2"] {
		t.Errorf("fresh = %v after restart, want SRE-2 still marked", second.fresh)
	}
}

// With the detail hidden, hovering is not reading.
func TestHoverKeepsFreshWhilePreviewHidden(t *testing.T) {
	v := &View{fresh: map[string]bool{"SRE-1": true}, unreadOn: true}
	v.list = ui.NewList[issue]()
	v.Update(ui.PreviewShownMsg(false))
	if !v.fresh["SRE-1"] {
		t.Errorf("fresh = %v, want the mark kept while the detail is hidden", v.fresh)
	}
}

// Leaving a viewed issue must not read the one you land on: the selection
// has already moved by the time the move is handled.
func TestMovingOnReadsTheIssueYouLeft(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	v.Update(ui.UnreadMsg(true))
	v.Update(ui.PreviewShownMsg(true))
	v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1"}}, source: v.defaultSource})
	v.Update(loadedMsg{issues: []issue{
		{Identifier: "SRE-1"}, {Identifier: "SRE-2"}, {Identifier: "SRE-3"},
	}, source: v.defaultSource})
	if !v.fresh["SRE-2"] || !v.fresh["SRE-3"] {
		t.Fatalf("setup: want SRE-2 and SRE-3 marked, got %v", v.fresh)
	}

	v.Update(tea.KeyPressMsg{Code: 'j'}) // leaves SRE-1, lands on SRE-2
	if !v.fresh["SRE-2"] {
		t.Errorf("landing on an issue read it: fresh = %v", v.fresh)
	}
}
