package linear

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

var inboxSource = navSource{Kind: "inbox", Label: "Inbox"}

// run executes a command and whatever it batches.
func run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			run(c)
		}
	}
}

func fakeLinear(t *testing.T) (bodies func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{"notificationUpdate":{"success":true}}}`))
	}))
	old := endpoint
	endpoint = srv.URL
	t.Cleanup(func() { endpoint = old; srv.Close() })
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

func inboxView(t *testing.T, rows ...issue) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.source = inboxSource
	v.Update(loadedMsg{issues: rows, source: inboxSource})
	return v
}

func unread(id, notif, comment string) issue {
	return issue{Identifier: id, Title: id, InboxEvent: "commented", InboxUnread: true, NotifID: notif, NotifCommentID: comment}
}

func readOnce(t *testing.T, bodies []string, notif string) {
	t.Helper()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "notificationUpdate") || !strings.Contains(bodies[0], `"id":"`+notif+`"`) || !strings.Contains(bodies[0], "readAt") {
		t.Fatalf("mutations sent = %v, want one notificationUpdate readAt for %s", bodies, notif)
	}
}

func TestSpaceMarksInboxRowRead(t *testing.T) {
	sent := fakeLinear(t)
	v := inboxView(t, unread("SRE-1", "n1", ""))

	cmd := v.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if v.list.Selected().InboxUnread {
		t.Error("row still unread after space")
	}
	run(cmd)
	readOnce(t, sent(), "n1")

	run(v.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}))
	if n := len(sent()); n != 1 {
		t.Errorf("%d mutations after a second space, want the row left alone", n)
	}
}

func TestSpaceOutsideInboxDoesNothing(t *testing.T) {
	sent := fakeLinear(t)
	v := inboxView(t, issue{Identifier: "SRE-1", Title: "t"})
	run(v.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}))
	if n := len(sent()); n != 0 {
		t.Errorf("%d mutations for a plain issue", n)
	}
}

func TestOpeningDetailMarksInboxRowRead(t *testing.T) {
	sent := fakeLinear(t)
	v := inboxView(t, unread("SRE-1", "n1", ""), issue{Identifier: "SRE-2", Title: "read", InboxEvent: "commented", NotifID: "n2"})

	run(v.Update(ui.PreviewShownMsg(true)))
	if v.list.Selected().InboxUnread {
		t.Error("row still unread after opening its detail")
	}
	readOnce(t, sent(), "n1")
}

func TestOpeningDetailOfReadRowSendsNothing(t *testing.T) {
	sent := fakeLinear(t)
	v := inboxView(t, issue{Identifier: "SRE-2", Title: "read", InboxEvent: "commented", NotifID: "n2"})
	run(v.Update(ui.PreviewShownMsg(true)))
	if n := len(sent()); n != 0 {
		t.Errorf("%d mutations for an already read row", n)
	}
}

func commentsFor(id string) map[string]*commentsState {
	list := make([]issueComment, 3)
	for i, n := range []string{"alice", "bob", "carol"} {
		list[i].ID = "c" + string(rune('1'+i))
		list[i].Body = "body of " + n
		list[i].User.DisplayName = n
	}
	return map[string]*commentsState{id: {done: true, list: list}}
}

// A comment notification opens the description, then the comments over it
// scrolled to that comment; left steps back to the description.
func TestCommentNotificationOpensCommentsAtTheComment(t *testing.T) {
	fakeLinear(t)
	v := inboxView(t, unread("SRE-1", "n1", "c3"))
	v.comments = commentsFor("SRE-1")

	v.Update(ui.PreviewShownMsg(true))
	v.Update(ui.PreviewFloatingMsg(true))
	if !v.showComments || !v.floatBase || !v.PaneFocused() {
		t.Fatalf("comments %v floatBase %v focused %v, want all true", v.showComments, v.floatBase, v.PaneFocused())
	}
	line, ok := v.TakePreviewJump()
	if !ok {
		t.Fatal("no scroll requested")
	}
	lines := strings.Split(ansi.Strip(v.PreviewView()), "\n")
	if !strings.Contains(lines[line-1], "@carol") {
		t.Errorf("scrolled to %q, want the line of @carol", lines[line-1])
	}

	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if v.showComments || !v.floatReveal {
		t.Errorf("left: comments %v float %v, want the description in the float", v.showComments, v.floatReveal)
	}
}

func TestOtherNotificationOpensDescription(t *testing.T) {
	fakeLinear(t)
	v := inboxView(t, unread("SRE-1", "n1", ""))
	v.Update(ui.PreviewFloatingMsg(true))
	v.Update(ui.PreviewShownMsg(true))
	if v.showComments {
		t.Error("a notification with no comment opened the comments")
	}
}

func TestSourceSurvivesRestart(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.LinearConfig{Token: "t", Triage: true}

	first := New(cfg, nil, nil, nil)
	first.navSel = first.navIndex(inboxSource)
	first.applyNav()

	second := New(cfg, nil, nil, nil)
	second.RestoreSource()
	if second.source != inboxSource {
		t.Fatalf("source = %+v, want the inbox", second.source)
	}
	if got := second.navItems[second.navSel].source; got != inboxSource {
		t.Errorf("tree cursor on %+v, want the inbox", got)
	}
	if second.list.Total() != 0 || !second.FetchesLocally() {
		t.Error("the default source's cache was left under another source")
	}
}

func TestVanishedSourceFallsBack(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first := New(config.LinearConfig{Token: "t", Triage: true}, nil, nil, nil)
	first.navSel = first.navIndex(navSource{Kind: "triage"})
	first.applyNav()

	second := New(config.LinearConfig{Token: "t"}, nil, nil, nil) // triage switched off
	second.RestoreSource()
	if second.source != second.defaultSource {
		t.Errorf("source = %+v, want the default", second.source)
	}

	proj := navSource{Kind: "project", ProjectID: "gone", Label: "Gone"}
	second.source, second.restoring = proj, true
	second.Update(favsMsg{projects: []navProject{{ID: "p1", Name: "P"}}})
	if second.source != second.defaultSource {
		t.Errorf("a project missing from the favourites kept the source: %+v", second.source)
	}
}
