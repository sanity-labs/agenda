package linear

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func focusView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1", Title: "t"}}, source: v.defaultSource})
	// The preview pane is on: both are visible, so focus is asked for.
	v.Update(ui.PreviewShownMsg(true))
	return v
}

// The nav tree takes the left arrow, so the preview takes the right:
// symmetric, and the same gesture the PRs view uses.
func TestRightFocusesThePreview(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'}) // show comments: something to scroll
	if !v.showComments {
		t.Fatal("setup: comments are not showing")
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !v.PaneFocused() {
		t.Error("right did not focus the preview")
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if v.PaneFocused() {
		t.Error("left did not return focus to the list")
	}
}

// Nothing scrollable, nothing to focus: the arrows stay with the list.
func TestNoFocusWithoutComments(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if v.PaneFocused() {
		t.Error("the preview took focus with nothing in it")
	}
}

// The list dims for either focus target, since two lit cursors say
// nothing about which one the arrows move.
func TestListDimsForBothFocusTargets(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	lit := v.ListView()

	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if dim := v.ListView(); !strings.Contains(dim, "\x1b[2m") {
		t.Errorf("the list is not dimmed with the pane focused:\n%s", ansi.Strip(dim))
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if back := v.ListView(); strings.Contains(back, "\x1b[2m") != strings.Contains(lit, "\x1b[2m") {
		t.Error("the list did not undim when focus came back")
	}
}

// esc steps back: focus first, then the comments pane.
func TestEscStepsBackThroughFocus(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})

	if !v.Dismiss() || v.PaneFocused() {
		t.Error("the first esc did not drop focus")
	}
	if !v.showComments {
		t.Error("the first esc closed the pane as well")
	}
	if !v.Dismiss() || v.showComments {
		t.Error("the second esc did not close the comments pane")
	}
	if v.Dismiss() {
		t.Error("esc claimed to act with nothing left open")
	}
}

// withComments gives i n comments, the way the list query reports them.
func withComments(i issue, n int) issue {
	i.Comments.Nodes = make([]struct {
		ID string `json:"id"`
	}, n)
	return i
}

func floatIssue(t *testing.T, comments int) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	i := withComments(issue{Identifier: "SRE-1", Title: "t"}, comments)
	v.Update(loadedMsg{issues: []issue{i}, source: v.defaultSource})
	v.Update(ui.PreviewShownMsg(false)) // startup, hide_preview
	return v
}

// pressV does what the root model does for 'v': tells the view to focus,
// then the two preview messages land.
func pressV(v *View) {
	v.FocusPane(true)
	v.Update(ui.PreviewShownMsg(true))
	v.Update(ui.PreviewFloatingMsg(true))
}

func closesFloat(t *testing.T, cmd tea.Cmd, what string) {
	t.Helper()
	if cmd == nil {
		t.Errorf("%s did nothing", what)
		return
	}
	if _, ok := cmd().(ui.ConcealPreviewMsg); !ok {
		t.Errorf("%s did not close the float", what)
	}
}

// Floated, the comments pane takes the keys on open and esc closes the
// whole float outright, as in the PRs view: there is no list beside it to
// hand focus back to, and the arrows are what step a level at a time.
func TestFloatedCommentsTakeFocusAndEscCloses(t *testing.T) {
	v := floatIssue(t, 0)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	if !v.PaneFocused() || !v.floatReveal {
		t.Fatal("a floated comments pane did not take the keys")
	}
	if v.Dismiss() {
		t.Error("esc stepped instead of handing the close to the root model")
	}
	if v.showComments || v.PaneFocused() || v.floatReveal {
		t.Errorf("esc left comments=%v focused=%v float=%v", v.showComments, v.PaneFocused(), v.floatReveal)
	}
}

// 'v' then 'c' is two levels: left goes back to the floated description,
// which keeps the keys, and only the next left closes. 'c' straight from
// the list is level one, so left closes it. Right has nowhere deeper.
func TestFloatLevels(t *testing.T) {
	v := floatIssue(t, 0)
	pressV(v)
	if !v.PaneFocused() {
		t.Fatal("setup: the floated description is not focused")
	}
	v.Update(tea.KeyPressMsg{Code: 'c'})
	if !v.showComments || !v.PaneFocused() {
		t.Fatal("setup: 'c' over the description did not open focused comments")
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyRight}); cmd != nil || !v.showComments {
		t.Error("right in a float acted")
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyLeft}); cmd != nil || v.showComments || !v.floatReveal || !v.PaneFocused() {
		t.Errorf("left did not step back to the floated description: comments=%v float=%v focused=%v", v.showComments, v.floatReveal, v.PaneFocused())
	}
	closesFloat(t, v.Update(tea.KeyPressMsg{Code: tea.KeyLeft}), "left on the description")

	direct := floatIssue(t, 0)
	direct.Update(tea.KeyPressMsg{Code: 'c'})
	closesFloat(t, direct.Update(tea.KeyPressMsg{Code: tea.KeyLeft}), "left in level-one comments")
	toggled := floatIssue(t, 0)
	toggled.Update(tea.KeyPressMsg{Code: 'c'})
	closesFloat(t, toggled.Update(tea.KeyPressMsg{Code: 'c'}), "'c' again at level one")
}

// The row carries who has the issue and how much has been said about it,
// in the PRs view's shape: id · project · assignee, comment count on the
// right. Nobody assigned says so.
func TestRowShowsAssigneeAndCommentCount(t *testing.T) {
	i := withComments(issue{Identifier: "SRE-1", Title: "t"}, 3)
	i.Project.Name = "Platform"
	got := ansi.Strip(i.Render(100, false, ui.Highlighter{}))
	if !strings.Contains(got, "Unassigned") {
		t.Errorf("an unassigned issue does not say so:\n%s", got)
	}
	if !strings.Contains(got, ui.IconComment+"3") {
		t.Errorf("the comment count is missing:\n%s", got)
	}
	i.Assignee.DisplayName = "tiago"
	i.Comments.Nodes = nil
	got = ansi.Strip(i.Render(100, false, ui.Highlighter{}))
	if !strings.Contains(got, "SRE-1 · Platform · @tiago") {
		t.Errorf("metadata is not id · project · assignee:\n%s", got)
	}
	if strings.Contains(got, ui.IconComment) {
		t.Errorf("a quiet issue shows a comment cell:\n%s", got)
	}
}

// The preview is headed like the PRs view: a Description section, then a
// Comments section that says how many there are and how to open them, and
// the hint is clickable.
func TestPreviewHeadersAndClickableHint(t *testing.T) {
	v := floatIssue(t, 2)
	v.Update(ui.PreviewShownMsg(true))
	lines := strings.Split(ansi.Strip(v.PreviewView()), "\n")
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "Description") || !strings.Contains(text, "Comments") {
		t.Fatalf("the preview lacks the section headers:\n%s", text)
	}
	hint := -1
	for n, l := range lines {
		if strings.Contains(l, commentsMarker) {
			hint = n
		}
	}
	if hint < 0 {
		t.Fatalf("no clickable comments hint:\n%s", text)
	}
	if !strings.Contains(lines[hint], "2 ·") {
		t.Errorf("the hint does not carry the count: %q", lines[hint])
	}
	v.ClickPreview(hint, 0)
	if !v.showComments {
		t.Error("clicking the hint did not open comments")
	}
	none := floatIssue(t, 0)
	none.Update(ui.PreviewShownMsg(true))
	if got := ansi.Strip(none.PreviewView()); !strings.Contains(got, "none yet") {
		t.Errorf("an issue without comments does not say so:\n%s", got)
	}
}

// Three places can hold the keys with the tree shown. Left from a focused
// pane goes to the list, not past it to the tree; left in a float steps out
// of the float. Before this, the tree swallowed both.
func TestLeftWalksPaneListTreeInOrder(t *testing.T) {
	v := focusView(t) // preview on
	v.navShown = true
	v.Update(tea.KeyPressMsg{Code: 'c'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !v.PaneFocused() {
		t.Fatal("setup: the pane is not focused")
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if v.PaneFocused() || v.navFocus {
		t.Errorf("left from the pane: paneFocus=%v navFocus=%v, want the list", v.PaneFocused(), v.navFocus)
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !v.navFocus {
		t.Error("left from the list did not reach the tree")
	}

	f := floatIssue(t, 0)
	f.navShown = true
	f.Update(tea.KeyPressMsg{Code: 'c'})
	closesFloat(t, f.Update(tea.KeyPressMsg{Code: tea.KeyLeft}), "left in a float with the tree shown")
	if f.navFocus {
		t.Error("left in a float focused the tree instead of closing the float")
	}
}

// Toggling the tree closes whatever pane was open and lights the list up:
// the tree is a different place to be, not a fourth thing to arrow between.
func TestNavToggleClosesPanes(t *testing.T) {
	v := focusView(t)
	v.Update(tea.KeyPressMsg{Code: 'c'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	v.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if v.showComments || v.PaneFocused() {
		t.Errorf("ctrl+p left comments=%v focused=%v", v.showComments, v.PaneFocused())
	}
	if !v.navShown || !v.navFocus {
		t.Fatalf("ctrl+p showed the tree without focusing it: shown=%v focus=%v", v.navShown, v.navFocus)
	}

	f := floatIssue(t, 0)
	f.Update(tea.KeyPressMsg{Code: 'c'})
	cmd := f.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if f.floatReveal || f.PaneFocused() {
		t.Error("ctrl+p left the float up")
	}
	if cmd == nil {
		t.Fatal("ctrl+p over a float returned nothing to close it")
	}
}

// ctrl+p is "take me to the tree": with the tree already up (a permanently
// on tree, say) it moves the keys there rather than hiding it; pressed
// again with the tree focused, it hides it and the list has the keys.
func TestCtrlPFocusesAnOpenTree(t *testing.T) {
	v := focusView(t)
	v.navShown = true // as linear.nav: true leaves it
	v.resizeList()
	v.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !v.navShown || !v.navFocus {
		t.Errorf("ctrl+p on an open tree: shown=%v focus=%v, want it focused", v.navShown, v.navFocus)
	}
	v.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if v.navShown || v.navFocus {
		t.Errorf("ctrl+p on a focused tree: shown=%v focus=%v, want it hidden", v.navShown, v.navFocus)
	}
}
