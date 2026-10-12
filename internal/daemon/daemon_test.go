package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pair(t *testing.T, handle Handler) (*Server, *Client) {
	t.Helper()
	sock := shortSock(t, "a.sock")
	s, err := Listen(sock, handle)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return s, c
}

// A call is answered by id with its result; a handler error comes back as
// the error, with its code when it is a protocol one.
func TestCallRoundTrip(t *testing.T) {
	_, c := pair(t, func(_ context.Context, req Request) (any, error) {
		switch req.Method {
		case MethodStatus:
			return Status{Version: "v1", Protocol: Protocol, PID: 42}, nil
		case "boom":
			return nil, &Error{Code: "nope", Message: "not today"}
		}
		return nil, &Error{Code: "unknown_method", Message: req.Method}
	})
	var st Status
	if err := c.Call(MethodStatus, nil, &st); err != nil || st.PID != 42 || st.Protocol != Protocol {
		t.Fatalf("status call: %v %+v", err, st)
	}
	err := c.Call("boom", nil, nil)
	var e *Error
	if err == nil || !errorsAs(err, &e) || e.Code != "nope" {
		t.Fatalf("error call: %v", err)
	}
}

func errorsAs(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

// A broadcast reaches a connected client as an event, and events keep
// flowing between calls.
func TestBroadcastReachesClients(t *testing.T) {
	s, c := pair(t, func(context.Context, Request) (any, error) { return nil, nil })
	// The server registers the connection on its first read; a call makes
	// sure it is there before the broadcast.
	_ = c.Call("ping", nil, nil)
	s.Broadcast(EventCacheChanged, CacheChanged{Name: "prs"})
	select {
	case ev := <-c.Events():
		if ev.Event != EventCacheChanged || string(ev.Data) != `{"name":"prs"}` {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
	}
	if s.Clients() != 1 {
		t.Errorf("clients = %d, want 1", s.Clients())
	}
}

// Closing the server ends the client: Done closes, a call fails, the
// socket file is gone.
func TestServerCloseEndsClient(t *testing.T) {
	s, c := pair(t, func(context.Context, Request) (any, error) { return nil, nil })
	_ = c.Call("ping", nil, nil)
	sock := s.Addr()
	s.Close()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("client did not notice the server going")
	}
	if err := c.Call("ping", nil, nil); err == nil {
		t.Error("a call after close succeeded")
	}
	if _, err := Dial(sock); err == nil {
		t.Error("the socket still answers after Close")
	}
}

// A stale socket file is taken over; a live one is refused.
func TestListenReplacesStaleSocketOnly(t *testing.T) {
	sock := shortSock(t, "a.sock")
	first, err := Listen(sock, func(context.Context, Request) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go first.Serve(ctx)
	if _, err := Listen(sock, nil); err == nil {
		t.Fatal("a second daemon bound a live socket")
	}
	cancel()
	_ = first.ln.Close() // leave the file behind, as a crash would
	second, err := Listen(sock, func(context.Context, Request) (any, error) { return nil, nil })
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	second.Close()
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
