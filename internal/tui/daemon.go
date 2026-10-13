package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/cache"
	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/daemon"
	"github.com/sanity-labs/agenda/internal/ui"
)

// Two ways the root model relates to a daemon. As a client it paints from
// the cache the daemon keeps fresh, reloads a view when told, and leaves
// fetching and notifying to it. Headless, it is the daemon: the same views
// run their fetch loops with no screen, and what would have been drawn
// (toasts, message-log lines) goes out over the socket instead.

// localFetcher is a view whose data is on this machine (the Sessions
// scan), so a client still fetches it itself: cheap, no API, and the
// daemon's scan may have happened before this window existed.
type localFetcher interface {
	FetchesLocally() bool
}

// daemonRefreshDefault is the interval the daemon falls back to when the
// config sets none: a daemon that fetched once at start and never again
// would be a cache, not a daemon.
const daemonRefreshDefault = 5 * time.Minute

// seenReporter is a view that can say which rows the user looked at, so
// the daemon drops their unread marks rather than reviving them.
type seenReporter interface {
	ReportSeen(func(view string, ids []string))
}

// WithDaemon makes the model a client of c. exe is this binary, for a
// respawn when the connection goes and the config still asks for one.
func (m Model) WithDaemon(c *daemon.Client, exe string) Model {
	m.daemon, m.daemonExe = c, exe
	m.hookSeen()
	return m
}

// WithStartupStatus queues a message-log line for Init, for something
// main found out before the screen existed.
func (m Model) WithStartupStatus(msg ui.StatusMsg) Model {
	m.startup = append(m.startup, msg)
	return m
}

// withHeadless makes the model the daemon: emit carries what a screen
// would have shown to the connected clients.
func (m Model) withHeadless(emit func(event string, data any)) Model {
	m.headless, m.emit = true, emit
	return m
}

// hookSeen points every view's seen hook at the daemon.
func (m *Model) hookSeen() {
	c := m.daemon
	for _, v := range m.views {
		if r, ok := v.(seenReporter); ok {
			if c == nil {
				r.ReportSeen(nil)
				continue
			}
			r.ReportSeen(func(view string, ids []string) {
				go func() { _ = c.Call(daemon.MethodSeen, daemon.SeenParams{View: view, IDs: ids}, nil) }()
			})
		}
	}
}

// Messages the client side moves on.
type (
	daemonEventMsg struct{ ev daemon.Event }
	daemonGoneMsg  struct{}
	daemonRetryMsg struct{}
	daemonBackMsg  struct{ c *daemon.Client }
	// refreshRequestMsg reaches the daemon: a client asked for a refetch.
	refreshRequestMsg struct{ view string }
)

// daemonPump waits for the next push and hands it to Update; the handler
// re-arms it. A closed channel means the daemon went away.
func (m Model) daemonPump() tea.Cmd {
	c := m.daemon
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-c.Events()
		if !ok {
			return daemonGoneMsg{}
		}
		return daemonEventMsg{ev}
	}
}

// daemonRefresh asks the daemon to refetch one view (or all, when view is
// empty) instead of fetching here.
func (m Model) daemonRefresh(view string) tea.Cmd {
	c := m.daemon
	return func() tea.Msg {
		if err := c.Call(daemon.MethodRefresh, daemon.RefreshParams{View: strings.ToLower(view)}, nil); err != nil {
			return ui.Status(ui.SeverityWarn, "daemon", "refresh failed: "+err.Error(), "")
		}
		return nil
	}
}

func daemonRetryTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return daemonRetryMsg{} })
}

// updateDaemon handles the client-side messages. ok is false for a
// message that is not one of them.
func (m Model) updateDaemon(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case daemonEventMsg:
		cmd := m.daemonPump()
		switch msg.ev.Event {
		case daemon.EventCacheChanged:
			var d daemon.CacheChanged
			if daemon.Decode(msg.ev.Data, &d) == nil {
				next, bcmd := m.broadcast(ui.CacheChangedMsg{Name: d.Name})
				m = next.(Model)
				cmd = tea.Batch(cmd, bcmd)
			}
		case daemon.EventToast:
			var d daemon.Toast
			if daemon.Decode(msg.ev.Data, &d) == nil {
				next, tcmd := m.Update(ui.ToastMsg{Title: d.Title, Body: d.Body, Success: d.Success})
				m = next.(Model)
				cmd = tea.Batch(cmd, tcmd)
			}
		case daemon.EventStatus:
			var d daemon.StatusLine
			if daemon.Decode(msg.ev.Data, &d) == nil {
				next, scmd := m.Update(ui.StatusMsg{Severity: ui.Severity(d.Severity), Source: d.Source, Summary: d.Summary, Detail: d.Detail, At: d.At})
				m = next.(Model)
				cmd = tea.Batch(cmd, scmd)
			}
		case daemon.EventStopping:
			// The gone message follows when the socket closes.
		}
		return m, cmd, true
	case daemonGoneMsg:
		// Fetch for ourselves until it is back; the ticks were off while
		// the daemon had them. The daemon is not respawned here: a stop
		// was somebody's decision.
		m.daemon, m.daemonLost = nil, true
		m.hookSeen()
		m.refreshGen++
		// Fetch now as well as on the ticks: the cache is only as fresh as
		// the daemon's last write, and the next tick is an interval away.
		cmds := []tea.Cmd{daemonRetryTick(), spinnerTick()}
		for i, v := range m.views {
			cmds = append(cmds, v.Init(), m.refreshTick(i))
		}
		return m, tea.Batch(cmds...), true
	case daemonRetryMsg:
		if m.daemon != nil || !m.daemonLost {
			return m, nil, true
		}
		return m, func() tea.Msg {
			sock, err := daemon.SocketPath()
			if err != nil {
				return daemonRetryMsg{}
			}
			c, err := daemon.Dial(sock)
			if err != nil {
				time.Sleep(5 * time.Second)
				return daemonRetryMsg{}
			}
			return daemonBackMsg{c}
		}, true
	case daemonBackMsg:
		m.daemon, m.daemonLost = msg.c, false
		m.hookSeen()
		m.refreshGen++ // our own ticks stop; the daemon fetches again
		next, bcmd := m.broadcast(ui.CacheChangedMsg{Name: ui.CacheAll})
		m = next.(Model)
		return m, tea.Batch(m.daemonPump(), bcmd), true
	}
	return m, nil, false
}

// StopDaemonForReload stops the daemon this model is a client of and
// waits for its socket to go, so the process a reload execs spawns a
// fresh one that reads the config as it is now. A daemon that read the
// old config would keep notifying with the old sound and searches, which
// is the one thing a reload must not leave behind.
func (m Model) StopDaemonForReload() {
	if m.daemon == nil {
		return
	}
	_ = m.daemon.Call(daemon.MethodStop, nil, nil)
	select {
	case <-m.daemon.Done():
	case <-time.After(5 * time.Second):
	}
	sock, err := daemon.SocketPath()
	if err != nil {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := daemon.Dial(sock)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

// daemonMarker is the footer's word on the daemon: present, or lost.
func (m Model) daemonMarker() string {
	switch {
	case m.daemon != nil:
		return m.theme.footerSep.Render() + m.theme.footerDesc.Render("daemon")
	case m.daemonLost:
		return m.theme.footerSep.Render() + ui.Red.Render("daemon gone")
	}
	return ""
}

// ServeDaemon runs the views headless behind the socket until told to
// stop or signalled. Every cache write becomes a cache_changed event;
// toasts and message-log lines are pushed to the clients and logged.
func ServeDaemon(cfg config.Config, views []View, version string) error {
	sock, err := daemon.SocketPath()
	if err != nil {
		return err
	}
	log.SetFlags(log.LstdFlags)
	log.SetOutput(os.Stderr)

	var p *tea.Program
	started := time.Now()
	var server *daemon.Server
	names := make([]string, len(views))
	for i, v := range views {
		names[i] = strings.ToLower(v.Title())
	}
	server, err = daemon.Listen(sock, func(_ context.Context, req daemon.Request) (any, error) {
		switch req.Method {
		case daemon.MethodStatus:
			return daemon.Status{Version: version, Protocol: daemon.Protocol, PID: os.Getpid(),
				Started: started, Views: names, Clients: server.Clients()}, nil
		case daemon.MethodRefresh:
			var params daemon.RefreshParams
			_ = daemon.Decode(req.Params, &params)
			p.Send(refreshRequestMsg{view: params.View})
			return nil, nil
		case daemon.MethodSeen:
			var params daemon.SeenParams
			if err := daemon.Decode(req.Params, &params); err != nil {
				return nil, &daemon.Error{Code: "bad_params", Message: err.Error()}
			}
			p.Send(ui.SeenMsg{View: params.View, IDs: params.IDs})
			return nil, nil
		case daemon.MethodStop:
			server.Broadcast(daemon.EventStopping, nil)
			go p.Quit()
			return nil, nil
		}
		return nil, &daemon.Error{Code: "unknown_method", Message: req.Method}
	})
	if err != nil {
		return err
	}
	defer server.Close()
	cache.OnSave = func(name string) { server.Broadcast(daemon.EventCacheChanged, daemon.CacheChanged{Name: name}) }

	if cfg.Refresh.Every == 0 {
		cfg.Refresh.Every = config.Duration(daemonRefreshDefault)
		log.Printf("refresh.every is unset; the daemon refreshes every %s", daemonRefreshDefault)
	}
	g := &greeter{}
	server.OnConnect = g.greet
	m := New(cfg, views).WithVersion(version).withHeadless(func(event string, data any) {
		if server.Clients() == 0 && g.keep(data) {
			return
		}
		server.Broadcast(event, data)
	})
	p = tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Serve(ctx)
	log.Printf("agenda daemon %s listening on %s (pid %d)", version, sock, os.Getpid())
	_, err = p.Run()
	log.Printf("agenda daemon stopping")
	return err
}

// greeter is what a window that connects is told first: that every cache
// is the daemon's to repaint from (a window attaching to a running daemon
// painted whatever was on disk, an empty file included, and nothing else
// would end its loading state until the next save), and what it missed:
// warnings and errors raised with no window open, and the toasts, each
// as a log line and together as one toast.
type greeter struct {
	mu     sync.Mutex
	toasts []daemon.Toast
	lines  []daemon.StatusLine
}

// keep holds what a screen would have shown while no window was open.
// Reports whether it did; anything else still goes out normally.
func (g *greeter) keep(data any) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch d := data.(type) {
	case daemon.Toast:
		g.toasts = append(g.toasts, d)
		if len(g.toasts) > 20 {
			g.toasts = g.toasts[len(g.toasts)-20:]
		}
		return true
	case daemon.StatusLine:
		if d.Severity >= int(ui.SeverityWarn) {
			g.lines = append(g.lines, d)
			if len(g.lines) > 50 {
				g.lines = g.lines[len(g.lines)-50:]
			}
		}
		return true
	}
	return false
}

func (g *greeter) greet(send func(event string, data any)) {
	send(daemon.EventCacheChanged, daemon.CacheChanged{Name: ui.CacheAll})
	g.mu.Lock()
	toasts, lines := g.toasts, g.lines
	g.toasts, g.lines = nil, nil
	g.mu.Unlock()
	for _, l := range lines {
		send(daemon.EventStatus, l)
	}
	if len(toasts) == 0 {
		return
	}
	titles := make([]string, 0, len(toasts))
	for _, t := range toasts {
		send(daemon.EventStatus, daemon.StatusLine{Severity: int(ui.SeverityInfo), Source: "notify", Summary: t.Title, Detail: t.Body, At: time.Now()})
		titles = append(titles, t.Title)
	}
	send(daemon.EventToast, daemon.Toast{Title: fmt.Sprintf("%d while you were away", len(toasts)), Body: strings.Join(titles, " · ")})
}

// updateHeadless handles what only the daemon sees. ok is false for a
// message that is not one of them.
func (m Model) updateHeadless(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ui.ToastMsg:
		m.emit(daemon.EventToast, daemon.Toast{Title: msg.Title, Body: msg.Body, Success: msg.Success})
		log.Printf("toast: %s: %s", msg.Title, msg.Body)
		return m, nil, true
	case ui.StatusMsg:
		m.emit(daemon.EventStatus, daemon.StatusLine{Severity: int(msg.Severity), Source: msg.Source, Summary: msg.Summary, Detail: msg.Detail, At: msg.At})
		log.Printf("%s: %s", msg.Source, msg.Summary)
		return m, nil, true
	case refreshRequestMsg:
		var cmds []tea.Cmd
		for _, v := range m.views {
			if msg.view == "" || strings.ToLower(v.Title()) == msg.view {
				cmds = append(cmds, v.Init())
			}
		}
		return m, tea.Batch(cmds...), true
	case ui.SeenMsg:
		next, cmd := m.broadcast(msg)
		return next.(Model), cmd, true
	}
	return m, nil, false
}

// headlessSize is the layout the daemon's views are given: no screen,
// but the views size their lists and previews from it.
func (m *Model) headlessSize() {
	for _, v := range m.views {
		v.SetSize(120, 80, 40)
	}
}

func (m Model) startupCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.startup))
	for _, s := range m.startup {
		s := s
		cmds = append(cmds, func() tea.Msg { return s })
	}
	return cmds
}
