package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func unreadView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	v.Update(ui.UnreadMsg(true))
	return v
}

// The first load is everything, not "new": marking it would light up the
// whole list on startup.
func TestFirstLoadIsNotUnread(t *testing.T) {
	v := unreadView(t)
	v.Update(reviewListMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	if len(v.unread) != 0 {
		t.Errorf("unread = %v after the first load, want none", v.unread)
	}
}

// A PR that appears in a later fetch is marked, whether or not a
// notification fired.
func TestArrivalIsMarkedUnread(t *testing.T) {
	v := unreadView(t)
	v.Update(reviewListMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	v.Update(reviewListMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1"}, {Number: 2, URL: "u2"},
	}}})
	if !v.unread["u2"] {
		t.Errorf("the new PR was not marked: unread = %v", v.unread)
	}
	if v.unread["u1"] {
		t.Error("an already-seen PR was marked")
	}
}

// Selecting a row is what makes it read, while the detail is on screen.
func TestSelectingClearsUnread(t *testing.T) {
	v := unreadView(t)
	v.Update(ui.PreviewShownMsg(true))
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	v.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1"}, {Number: 2, URL: "u2"},
	}}})
	if len(v.unread) == 0 {
		t.Fatal("nothing marked, so the test proves nothing")
	}
	// Walk onto the new row.
	for i := 0; i < 3 && len(v.unread) > 0; i++ {
		v.Update(tea.KeyPressMsg{Code: 'j'})
	}
	if len(v.unread) != 0 {
		t.Errorf("unread = %v after selecting the rows, want cleared", v.unread)
	}
}

// The dot renders, and the gutter keeps rows aligned once read.
func TestUnreadRendersAndKeepsAlignment(t *testing.T) {
	marked := pr{Number: 1, URL: "u", Unread: true, UnreadGutter: true}
	marked.Repository.NameWithOwner = "o/r"
	read := marked
	read.Unread = false

	got := marked.Render(80, false, ui.Highlighter{})
	if !strings.Contains(got, ui.IconUnread) {
		t.Errorf("unread row has no mark:\n%s", got)
	}
	// Same width either way, or clearing a mark shifts the row.
	if a, b := lineWidth(got), lineWidth(read.Render(80, false, ui.Highlighter{})); a != b {
		t.Errorf("row widths differ: unread %d, read %d", a, b)
	}
}

// Display width, not byte length: the styled dot carries ANSI codes that a
// plain len() counts and a terminal does not.
func lineWidth(s string) int {
	return lipgloss.Width(strings.Split(s, "\n")[0])
}

func TestUnreadOffClearsMarks(t *testing.T) {
	v := unreadView(t)
	v.unread = map[string]bool{"u1": true}
	v.Update(ui.UnreadMsg(false))
	if len(v.unread) != 0 {
		t.Errorf("unread = %v after turning the feature off, want cleared", v.unread)
	}
}

// With the detail hidden, moving onto a row is not reading it: you have not
// seen anything yet, so the mark has to survive until you ask for the detail.
func TestHoverKeepsUnreadWhilePreviewHidden(t *testing.T) {
	v := unreadView(t)
	v.Update(ui.PreviewShownMsg(false))
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	v.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1"}, {Number: 2, URL: "u2"},
	}}})
	if len(v.unread) == 0 {
		t.Fatal("nothing marked, so the test proves nothing")
	}
	for i := 0; i < 3; i++ {
		v.Update(tea.KeyPressMsg{Code: 'j'})
	}
	if !v.unread["u2"] {
		t.Errorf("hovering cleared the mark with the detail hidden: unread = %v", v.unread)
	}
	// Revealing the detail shows the selected row, which reads it.
	v.Update(ui.PreviewShownMsg(true))
	if v.unread["u2"] {
		t.Errorf("revealing the detail left the mark: unread = %v", v.unread)
	}
}

// The bug this fixes: an unread mark you never looked at vanished on
// restart, because the mark lived only in memory while the rows it
// described were cached. Quitting silently marked everything read.
func TestUnreadSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	first := New(config.GitHubConfig{}, nil, nil, nil)
	first.SetSize(80, 60, 40)
	first.Update(ui.UnreadMsg(true))
	first.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	first.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1"}, {Number: 2, URL: "u2"},
	}}})
	if !first.unread["u2"] {
		t.Fatalf("nothing marked before the restart: unread = %v", first.unread)
	}

	// A second process against the same cache: the mark is still there.
	second := New(config.GitHubConfig{}, nil, nil, nil)
	second.SetSize(80, 60, 40)
	second.Update(ui.UnreadMsg(true))
	if !second.unread["u2"] {
		t.Errorf("unread = %v after restart, want u2 still marked", second.unread)
	}
	if second.unread["u1"] {
		t.Errorf("a row that was never new came back marked: %v", second.unread)
	}
}

// Reading has to persist too, or a mark you cleared returns on restart.
func TestClearedUnreadStaysClearedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	first := New(config.GitHubConfig{}, nil, nil, nil)
	first.SetSize(80, 60, 40)
	first.Update(ui.UnreadMsg(true))
	first.Update(ui.PreviewShownMsg(true))
	first.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	first.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1"}, {Number: 2, URL: "u2"},
	}}})
	for i := 0; i < 3 && len(first.unread) > 0; i++ {
		first.Update(tea.KeyPressMsg{Code: 'j'})
	}
	if len(first.unread) != 0 {
		t.Fatalf("the marks were not cleared before the restart: %v", first.unread)
	}

	second := New(config.GitHubConfig{}, nil, nil, nil)
	second.SetSize(80, 60, 40)
	second.Update(ui.UnreadMsg(true))
	if len(second.unread) != 0 {
		t.Errorf("unread = %v after restart, want the cleared marks to stay cleared", second.unread)
	}
}

// Leaving a floated row must not read the row you land on. The selection
// has already moved by the time the move is handled, and the conceal that
// ends the float is only dispatched afterwards, so a naive clear here
// reads the wrong row: press 'v', move on, and the next one is read
// before you have seen it.
func TestMovingOffAFloatDoesNotReadTheNextRow(t *testing.T) {
	v := unreadView(t)
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1"}}}})
	v.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1"}, {Number: 2, URL: "u2"}, {Number: 3, URL: "u3"},
	}}})
	if !v.unread["u2"] || !v.unread["u3"] {
		t.Fatalf("setup: want u2 and u3 marked, got %v", v.unread)
	}

	// The pane is off, so 'v' floats the detail for the selected row only.
	v.Update(ui.PreviewShownMsg(false))
	v.Update(tea.KeyPressMsg{Code: 'j'}) // onto u2, still unread: not seen
	if !v.unread["u2"] {
		t.Fatal("hovering read a row with the detail hidden")
	}
	v.Update(ui.PreviewShownMsg(true)) // 'v': the float opens on u2
	v.Update(ui.PreviewFloatingMsg(true))
	if v.unread["u2"] {
		t.Fatal("setup: opening the float should have read u2")
	}

	// Moving on closes the float. u3 has not been seen.
	v.Update(tea.KeyPressMsg{Code: 'j'})
	if !v.unread["u3"] {
		t.Errorf("moving off the float read the next row: unread = %v", v.unread)
	}
}
