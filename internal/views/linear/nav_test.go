package linear

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func navPress(r rune) tea.KeyPressMsg   { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func navSpecial(c rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: c} }

func TestSourceForScope(t *testing.T) {
	if s := sourceForScope(""); s.Kind != "assigned" || s.Label != "My Issues" {
		t.Errorf("default scope = %+v", s)
	}
	if s := sourceForScope("all"); s.Kind != "all" {
		t.Errorf("all scope = %+v", s)
	}
}

func TestRebuildNavAndMove(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.favs = []navProject{{ID: "p1", Name: "Peekaboo"}}
	v.rebuildNav()
	// Inbox, My Issues, All Issues, header, Peekaboo.
	if len(v.navItems) != 5 || !v.navItems[3].header {
		t.Fatalf("navItems = %+v", v.navItems)
	}
	if v.navItems[0].source.Kind != "inbox" {
		t.Errorf("first entry = %+v, want the inbox", v.navItems[0].source)
	}
	v.navSel = 2
	v.navMove(1) // skips the header onto the project
	if got := v.navItems[v.navSel].source.Label; got != "Peekaboo" {
		t.Errorf("navMove landed on %q", got)
	}
	v.navMove(1) // at the edge: stays
	if got := v.navItems[v.navSel].source.Label; got != "Peekaboo" {
		t.Errorf("edge move landed on %q", got)
	}
}

func TestNavSelectSwitchesSourceAndSilencesNotify(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, fakeNotifier{}, nil)
	v.seeded = true
	v.navShown, v.navFocus = true, true
	v.favs = []navProject{{ID: "p1", Name: "Peekaboo"}}
	v.rebuildNav()
	v.navSel = 2 // All Issues

	if cmd := v.updateNav(navSpecial(tea.KeyEnter)); cmd == nil {
		t.Fatal("selecting a new source should refetch")
	}
	if v.source.Kind != "all" || v.navFocus {
		t.Fatalf("source = %+v, focus = %v", v.source, v.navFocus)
	}

	// The switched-source load must not fire "new issue" notifications, but
	// a subsequent same-source load must.
	if cmd := v.notifySwitchProbe(t); cmd {
		t.Fatal("source switch produced a notify command")
	}
}

// notifySwitchProbe replays the loadedMsg flow for a source switch and then a
// same-source refresh, reporting whether the switch itself notified.
func (v *View) notifySwitchProbe(t *testing.T) bool {
	t.Helper()
	switchNotified := false
	if cmd := v.Update(loadedMsg{issues: mkIDs("A-1", "A-2"), source: v.source}); cmd != nil {
		switchNotified = true // lastLoaded was the old source: must be nil
	}
	// Same source again with a new issue: now it should notify.
	if cmd := v.Update(loadedMsg{issues: mkIDs("A-1", "A-2", "A-3"), source: v.source}); cmd == nil {
		t.Error("same-source refresh with a new issue should notify")
	}
	return switchNotified
}

func TestCtrlPTogglesNav(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.SetSize(80, 60, 30)
	if v.navShown {
		t.Fatal("nav should start hidden by default")
	}
	v.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !v.navShown {
		t.Fatal("ctrl+p should show the nav pane")
	}
	v.Update(navSpecial(tea.KeyLeft))
	if !v.navFocus {
		t.Error("left should focus the tree")
	}
	v.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if v.navShown || v.navFocus {
		t.Error("ctrl+p should hide the pane and drop focus")
	}
}

func TestMineToggleIsProjectContextual(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	// On the fixed sources, 'm' is a no-op.
	if cmd := v.Update(navPress('m')); cmd != nil || v.projectMine {
		t.Fatal("mine toggle should be inert outside a project source")
	}
	// On a project source it toggles and refetches.
	v.source = navSource{Kind: "project", ProjectID: "p1", Label: "Peekaboo"}
	if cmd := v.Update(navPress('m')); cmd == nil || !v.projectMine {
		t.Fatal("mine toggle should refetch on a project source")
	}
	v.loading = false // status shows the source once the fetch settles
	if !strings.Contains(v.statusText(), "Peekaboo (mine)") {
		t.Errorf("status = %q, want the (mine) marker", v.statusText())
	}
	if cmd := v.Update(navPress('m')); cmd == nil || v.projectMine {
		t.Fatal("second toggle should clear and refetch")
	}
}

func TestInboxEventLabel(t *testing.T) {
	cases := map[string]string{
		"issueNewComment":    "commented",
		"issueStatusChanged": "status changed",
		"issueAssignedToYou": "assigned to you",
		"issueWeirdNewThing": "weird new thing", // fallback humanizer
	}
	for in, want := range cases {
		if got := inboxEventLabel(in); got != want {
			t.Errorf("inboxEventLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInboxRowRendering(t *testing.T) {
	i := issue{Identifier: "SRE-1", Title: "A thing", InboxEvent: "commented",
		InboxActor: "nick", InboxUnread: true}
	out := i.Render(60, false, ui.Highlighter{})
	for _, want := range []string{"●", "commented", "nick", "SRE-1", "A thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("inbox row missing %q:\n%s", want, out)
		}
	}
	i.InboxUnread = false
	if !strings.Contains(i.Render(60, false, ui.Highlighter{}), "○") {
		t.Error("read row missing the hollow marker")
	}
}

func TestCommentsKeyCycle(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.SetSize(80, 60, 30)
	v.raw = mkIDs("A-1")
	v.applySort()

	// Hidden -> show and request the jump.
	v.Update(navPress('c'))
	if !v.showComments || !v.jumpPending {
		t.Fatalf("first c: showComments=%v jumpPending=%v", v.showComments, v.jumpPending)
	}
	if line, ok := v.TakePreviewJump(); !ok || line <= 0 {
		t.Fatalf("jump not taken: line=%d ok=%v", line, ok)
	}
	if _, ok := v.TakePreviewJump(); ok {
		t.Fatal("jump should be consumed once")
	}

	// Visible and already jumped -> hide.
	v.Update(navPress('c'))
	if v.showComments {
		t.Fatal("second c after jumping should hide")
	}

	// Settings-enabled case: visible but never jumped -> jump, not hide.
	v.showComments, v.commentsJumped = true, false
	v.Update(navPress('c'))
	if !v.showComments || !v.jumpPending {
		t.Fatal("c on settings-enabled comments should jump, not hide")
	}

	// Moving the selection restarts the cycle.
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if v.commentsJumped {
		t.Fatal("selection change should reset the jump cycle")
	}
}

func TestNavItemAtMatchesRenderedTree(t *testing.T) {
	ui.SetGlyphs(false)
	defer ui.SetGlyphs(true)
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.navShown = true
	v.favs = []navProject{{ID: "p1", Name: "Peekaboo"}}
	v.rebuildNav()
	v.SetSize(80, 60, 20)

	lines := strings.Split(v.navView(), "\n")
	for y, line := range lines {
		i := v.navItemAt(y)
		if i < 0 {
			continue
		}
		if label := v.navItems[i].source.Label; !strings.Contains(line, label) {
			t.Errorf("navItemAt(%d) = %q, but that line renders %q", y, label, line)
		}
	}
	// Every entry is reachable by a click; headers and the gaps are not.
	hits := map[int]bool{}
	for y := range lines {
		if i := v.navItemAt(y); i >= 0 {
			hits[i] = true
		}
	}
	for i, it := range v.navItems {
		if hits[i] == it.header {
			t.Errorf("entry %d (%q, header=%v): clickable=%v", i, it.source.Label, it.header, hits[i])
		}
	}
}

func TestClickListRoutesTreeAndRows(t *testing.T) {
	ui.SetGlyphs(false)
	defer ui.SetGlyphs(true)
	v := New(config.LinearConfig{Token: "x"}, nil, fakeNotifier{}, nil)
	v.navShown, v.navFocus = true, true
	v.rebuildNav()
	v.SetSize(80, 60, 20)
	v.raw = mkIDs("A-1", "A-2")
	v.applySort()

	// "All Issues" is the third entry: line 3 of the tree.
	row, cmd := v.ClickList(1, 3)
	if row || cmd == nil || v.source.Kind != "all" {
		t.Fatalf("tree click: row=%v cmd=%v source=%+v, want a refetch of All Issues", row, cmd != nil, v.source)
	}

	v.navFocus = true
	navW := lipgloss.Width(v.navView())
	if row, _ := v.ClickList(navW+1, 0); row {
		t.Error("click on the list header counted as a row")
	}
	if row, _ := v.ClickList(navW+1, 3); !row || v.list.Selected().Identifier != "A-2" {
		t.Errorf("click on the second row selected %q (row=%v), want A-2", v.list.Selected().Identifier, row)
	}
	if v.navFocus {
		t.Error("clicking a row left focus in the tree")
	}
}
