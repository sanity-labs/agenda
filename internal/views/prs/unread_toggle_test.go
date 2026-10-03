package prs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The "mark new items" toggle is display only. A row that arrives while it
// is off is still recorded, so turning it on later shows what came in; a
// mark recorded only while the toggle was on would make the setting a
// window you have to be standing in.
func TestNewRowsAreRecordedWhileMarksAreOff(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.Update(ui.UnreadMsg(false))
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1", Title: "one"}}}})
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1", Title: "one"}, {Number: 2, URL: "u2", Title: "two"}}}})

	if !v.unread["u2"] {
		t.Fatal("a row that arrived with marks off was not recorded")
	}
	if strings.Contains(ansi.Strip(v.ListView()), ui.IconUnread) {
		t.Error("the mark is drawn while the toggle is off")
	}

	v.Update(ui.UnreadMsg(true))
	if !strings.Contains(ansi.Strip(v.ListView()), ui.IconUnread) {
		t.Error("turning marks on did not show the row that arrived while they were off")
	}
	v.Update(ui.UnreadMsg(false))
	if !v.unread["u2"] {
		t.Error("turning marks off threw the recorded marks away")
	}
}
