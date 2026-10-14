package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/daemon"
	"github.com/sanity-labs/agenda/internal/ui"
)

// recView records the messages the root model hands it.
type recView struct {
	fatView
	got   []tea.Msg
	seen  func(string, []string)
	inits int
	local bool
}

func (r *recView) Update(msg tea.Msg) tea.Cmd { r.got = append(r.got, msg); return nil }
func (r *recView) Init() tea.Cmd              { r.inits++; return nil }
func (r *recView) FetchesLocally() bool       { return r.local }

// A view whose data is local (Sessions) still scans for itself in a
// client: the daemon's scan may predate this window.
func TestClientStillFetchesLocalViews(t *testing.T) {
	s, m, v, _ := daemonPair(t, nil)
	// Init's batch holds the event pump, which blocks on the socket; with
	// the server gone it returns at once and the batch can be walked.
	s.Close()
	<-m.daemon.Done()
	runBatch(m.Init())
	if v.inits != 0 {
		t.Fatalf("a client ran Init %d times on a view the daemon fetches for", v.inits)
	}
	v.local = true
	runBatch(m.Init())
	if v.inits != 1 {
		t.Fatalf("local view Init ran %d times in a client, want 1", v.inits)
	}
}

// runBatch runs a command tree far enough for synchronous Inits to count.
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			runBatch(c)
		}
	}
}
func (r *recView) ReportSeen(f func(view string, ids []string)) { r.seen = f }

// daemonPair is a live server on a temp socket, a client on it, and a
// model that is that client.
func daemonPair(t *testing.T, handle daemon.Handler) (*daemon.Server, Model, *recView, chan daemon.Request) {
	t.Helper()
	calls := make(chan daemon.Request, 8)
	sock := shortSock(t, "a.sock")
	s, err := daemon.Listen(sock, func(ctx context.Context, req daemon.Request) (any, error) {
		calls <- req
		if handle != nil {
			return handle(ctx, req)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	c, err := daemon.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	v := &recView{fatView: fatView{title: "PRs"}}
	m := New(config.Default(), []View{v}).WithDaemon(c, "")
	m.width, m.height, m.ready = 100, 30, true
	m.layout()
	return s, m, v, calls
}

// A client model starts no fetch of its own, and a cache_changed push
// reaches the view that owns the name as a CacheChangedMsg.
func TestClientRepaintsWhenTheDaemonWrites(t *testing.T) {
	s, m, v, calls := daemonPair(t, nil)
	if cmd := m.refreshTick(0); cmd != nil {
		t.Error("a client scheduled its own refresh tick")
	}
	_ = m.daemon.Call("ping", nil, nil) // registers the connection server-side
	<-calls
	s.Broadcast(daemon.EventCacheChanged, daemon.CacheChanged{Name: "prs"})
	msg := m.daemonPump()()
	got, _ := m.Update(msg)
	m = got.(Model)
	found := false
	for _, g := range v.got {
		if c, ok := g.(ui.CacheChangedMsg); ok && c.Name == "prs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("view never got CacheChangedMsg{prs}; got %v", v.got)
	}
	if !strings.Contains(m.View().Content, "daemon") {
		t.Error("footer does not say a daemon is connected")
	}
}

// ctrl+r asks the daemon to refetch the current view rather than
// fetching here; rows the user looks at are reported as seen.
func TestClientAsksTheDaemonToRefreshAndReportsSeen(t *testing.T) {
	_, m, v, calls := daemonPair(t, nil)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+r returned no command")
	}
	cmd()
	select {
	case req := <-calls:
		if req.Method != daemon.MethodRefresh || !strings.Contains(string(req.Params), `"prs"`) {
			t.Fatalf("daemon got %s %s, want a refresh of prs", req.Method, req.Params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the daemon was not asked to refresh")
	}
	if v.seen == nil {
		t.Fatal("the view's seen hook was not set")
	}
	v.seen("prs", []string{"u1"})
	select {
	case req := <-calls:
		if req.Method != daemon.MethodSeen || !strings.Contains(string(req.Params), `"u1"`) {
			t.Fatalf("daemon got %s %s, want seen u1", req.Method, req.Params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("seen rows were not reported")
	}
}

// When the daemon goes, the client fetches for itself and says so; when
// one is back it hands fetching over again and repaints.
func TestClientFallsBackWhenTheDaemonGoes(t *testing.T) {
	s, m, v, _ := daemonPair(t, nil)
	s.Close()
	msg := m.daemonPump()()
	if g, ok := msg.(daemonGoneMsg); !ok || g.c != m.daemon {
		t.Fatalf("pump returned %#v, want daemonGoneMsg for the current connection", msg)
	}
	got, cmd := m.Update(msg)
	m = got.(Model)
	if m.daemon != nil || !m.daemonLost || cmd == nil {
		t.Fatalf("after gone: daemon=%v lost=%v cmd=%v", m.daemon != nil, m.daemonLost, cmd != nil)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "daemon gone") {
		t.Error("footer does not say the daemon is gone")
	}
	if v.seen != nil {
		t.Error("seen hook still set with no daemon")
	}
	sock := shortSock(t, "b.sock")
	s2, err := daemon.Listen(sock, func(context.Context, daemon.Request) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s2.Serve(ctx)
	t.Cleanup(func() { cancel(); s2.Close() })
	c, err := daemon.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = m.Update(daemonBackMsg{c: c})
	m = got.(Model)
	if m.daemon == nil || m.daemonLost {
		t.Fatal("the model did not take the daemon back")
	}
	// A gone notice from the old connection must not undo the new one.
	got, _ = m.Update(daemonGoneMsg{c: nil})
	m = got.(Model)
	if m.daemon == nil || m.daemonLost {
		t.Fatal("a stale gone notice dropped the new connection")
	}
	if n := len(v.got); n == 0 {
		t.Fatal("no repaint asked of the view on reconnect")
	} else if c, ok := v.got[n-1].(ui.CacheChangedMsg); !ok || !c.Matches("prs") {
		t.Errorf("last view msg = %v, want a CacheChangedMsg every view answers", v.got[n-1])
	}
}

// Headless, what a screen would show goes out as events: a toast, a
// message-log line; a refresh request runs the named view's Init.
func TestHeadlessEmitsInsteadOfDrawing(t *testing.T) {
	var events []string
	v := &recView{fatView: fatView{title: "PRs"}}
	m := New(config.Default(), []View{v}).withHeadless(func(event string, _ any) { events = append(events, event) })
	got, _ := m.Update(ui.ToastMsg{Title: "t", Body: "b"})
	m = got.(Model)
	got, _ = m.Update(ui.Status(ui.SeverityWarn, "PRs", "s", ""))
	m = got.(Model)
	if strings.Join(events, ",") != "toast,status" {
		t.Fatalf("events = %v", events)
	}
	if m.toast != nil || len(m.status) != 0 {
		t.Error("headless model kept a toast or status for a screen it has not got")
	}
	got, _ = m.Update(ui.SeenMsg{View: "prs", IDs: []string{"u"}})
	m = got.(Model)
	if len(v.got) == 0 {
		t.Error("seen rows were not handed to the views")
	}
}

// shortSock is a socket path under /tmp: a t.TempDir() path is longer
// than a unix socket path may be on macOS.
func shortSock(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agenda-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// A reload stops the daemon first, so the new process spawns one on the
// new config instead of attaching to one that read the old.
func TestReloadStopsTheDaemon(t *testing.T) {
	var s *daemon.Server
	s, m, _, calls := daemonPair(t, func(_ context.Context, req daemon.Request) (any, error) {
		if req.Method == daemon.MethodStop {
			go func() { time.Sleep(50 * time.Millisecond); s.Close() }()
		}
		return nil, nil
	})
	m.StopDaemonForReload()
	select {
	case req := <-calls:
		if req.Method != daemon.MethodStop {
			t.Fatalf("daemon got %s, want stop", req.Method)
		}
	default:
		t.Fatal("the daemon was not asked to stop")
	}
	select {
	case <-m.daemon.Done():
	default:
		t.Fatal("StopDaemonForReload returned before the connection was gone")
	}
}

// A window that connects is first told to repaint every cache (so one that
// painted an empty file still leaves its loading state), then given what
// it missed: warnings as log lines, toasts folded into one.
func TestGreeterRepaintsThenReplays(t *testing.T) {
	g := &greeter{}
	if !g.keep(daemon.Toast{Title: "review requested"}) || !g.keep(daemon.Toast{Title: "assigned"}) {
		t.Fatal("toasts were not kept")
	}
	if !g.keep(daemon.StatusLine{Severity: int(ui.SeverityWarn), Summary: "forbidden"}) {
		t.Fatal("a warning was not kept")
	}
	if g.keep(daemon.CacheChanged{Name: "prs"}) {
		t.Fatal("a cache change must go out, not be kept")
	}
	var events []string
	g.greet(func(event string, data any) {
		if c, ok := data.(daemon.CacheChanged); ok {
			event += ":" + c.Name
		}
		events = append(events, event)
	})
	want := "cache_changed:*,status,status,status,toast"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("greeting = %s, want %s", got, want)
	}
	events = nil
	g.greet(func(event string, _ any) { events = append(events, event) })
	if strings.Join(events, ",") != "cache_changed" {
		t.Fatalf("second greeting replayed again: %v", events)
	}
}

// A config write the daemon acts on (a search edited with F) restarts it,
// so it fetches on the new search rather than bringing excluded rows back
// and notifying for them. A write it does not act on leaves it alone.
func TestConfigWriteTheDaemonFollowsRestartsIt(t *testing.T) {
	_, m, _, calls := daemonPair(t, nil)
	got, cmd := m.Update(ui.ConfigSetMsg{Path: "github.filter", Value: "is:pr -author:x"})
	m = got.(Model)
	if cmd == nil {
		t.Fatal("no command for a config write")
	}
	runBatch(cmd)
	stopped := false
	for len(calls) > 0 {
		if req := <-calls; req.Method == daemon.MethodStop {
			stopped = true
		}
	}
	if !stopped {
		t.Fatal("the daemon was not asked to stop after the search changed")
	}
	if cmd := m.restartDaemon("x"); cmd == nil {
		t.Fatal("restartDaemon returned nothing with a daemon connected")
	}
	if !daemonFollows("github.filter") || !daemonFollows("refresh.every") || daemonFollows("mouse") || daemonFollows("unread") {
		t.Error("daemonFollows picks the wrong keys")
	}
}
