package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/daemon"
)

// status says not running and exits 1 with no daemon; with one on the
// socket it reports its state, and stop takes it down.
func TestStatusAndStopAgainstADaemon(t *testing.T) {
	// A unix socket path is capped at about 100 bytes on macOS, which a
	// t.TempDir() path exceeds; /tmp keeps it short.
	dir, err := os.MkdirTemp("/tmp", "agenda-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_STATE_HOME", dir)
	var out bytes.Buffer
	if code := runStatus(&out, nil); code != 1 || !strings.Contains(out.String(), "not running") {
		t.Fatalf("status with no daemon: code=%d\n%s", code, out.String())
	}

	sock, _ := daemon.SocketPath()
	stopped := make(chan struct{})
	var s *daemon.Server
	s, err = daemon.Listen(sock, func(_ context.Context, req daemon.Request) (any, error) {
		switch req.Method {
		case daemon.MethodStatus:
			return daemon.Status{Version: versionString(), Protocol: daemon.Protocol, PID: 7, Started: time.Now(), Views: []string{"prs"}}, nil
		case daemon.MethodStop:
			go func() { time.Sleep(50 * time.Millisecond); s.Close(); close(stopped) }()
			return nil, nil
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx)

	out.Reset()
	if code := runStatus(&out, nil); code != 0 || !strings.Contains(out.String(), "status: running") || !strings.Contains(out.String(), "pid: 7") {
		t.Fatalf("status with a daemon: code=%d\n%s", code, out.String())
	}
	out.Reset()
	if code := runStop(&out, nil); code != 0 || !strings.Contains(out.String(), "daemon stopped") {
		t.Fatalf("stop: code=%d\n%s", code, out.String())
	}
	<-stopped
}
