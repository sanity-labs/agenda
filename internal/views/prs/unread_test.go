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

// Selecting a row is what makes it read.
func TestSelectingClearsUnread(t *testing.T) {
	v := unreadView(t)
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
