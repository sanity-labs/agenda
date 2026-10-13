package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Handler answers one request. Returning an *Error sends it as such; any
// other error becomes code "internal".
type Handler func(ctx context.Context, req Request) (any, error)

// Server listens on the socket, answers requests through the handler and
// fans events out to every connected client.
type Server struct {
	ln      net.Listener
	handle  Handler
	mu      sync.Mutex
	clients map[net.Conn]*bufio.Writer
	// OnConnect, when set, runs for each new client with a sender that
	// reaches only that client: what it missed while away goes here.
	OnConnect func(send func(event string, data any))
}

// Listen binds the socket. A socket file nobody answers on (a daemon that
// died) is removed first; one that answers means another daemon is up,
// and that is an error rather than a takeover.
func Listen(path string, handle Handler) (*Server, error) {
	// sun_path is 104 bytes on macOS and 108 on Linux; a long state dir
	// fails with "invalid argument", which says nothing.
	if len(path) > 100 {
		return nil, errors.New("socket path too long for a unix socket: " + path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if c, err := net.Dial("unix", path); err == nil {
		_ = c.Close()
		return nil, errors.New("another daemon is listening on " + path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return &Server{ln: ln, handle: handle, clients: map[net.Conn]*bufio.Writer{}}, nil
}

// Addr is the socket path the server is bound to.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Serve accepts connections until ctx ends or the listener closes.
func (s *Server) Serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		// Registered here, not in the goroutine: a Close between the accept
		// and the goroutine's first line would otherwise miss this
		// connection and leave its client reading forever.
		s.mu.Lock()
		s.clients[conn] = bufio.NewWriter(conn)
		s.mu.Unlock()
		go s.serveConn(ctx, conn)
	}
}

// Close drops every client and the socket file.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.clients {
		_ = c.Close()
	}
	s.clients = map[net.Conn]*bufio.Writer{}
	s.mu.Unlock()
	_ = os.Remove(s.Addr())
}

// Clients is how many connections are open.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Broadcast pushes an event to every client. A client that cannot be
// written to is dropped; it will reconnect or it is gone.
func (s *Server) Broadcast(event string, data any) {
	line, _ := json.Marshal(Event{Event: event, Data: marshal(data)})
	line = append(line, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, w := range s.clients {
		// A client that stopped draining its socket must not stall every
		// other one: bound the write, and drop it when it does not keep up.
		_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := w.Write(line); err != nil || w.Flush() != nil {
			_ = c.Close()
			delete(s.clients, c)
		}
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	if s.OnConnect != nil {
		s.OnConnect(func(event string, data any) { s.send(conn, Event{Event: event, Data: marshal(data)}) })
	}
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			s.reply(conn, Response{Error: &Error{Code: "bad_request", Message: err.Error()}})
			continue
		}
		result, err := s.handle(ctx, req)
		resp := Response{ID: req.ID}
		var e *Error
		switch {
		case errors.As(err, &e):
			resp.Error = e
		case err != nil:
			resp.Error = &Error{Code: "internal", Message: err.Error()}
		default:
			resp.Result = marshal(result)
		}
		s.reply(conn, resp)
	}
}

// reply writes one response under the clients lock, so it never
// interleaves with a broadcast on the same connection.
func (s *Server) reply(conn net.Conn, resp Response) { s.send(conn, resp) }

// send writes one line to one client under the clients lock.
func (s *Server) send(conn net.Conn, v any) {
	line, _ := json.Marshal(v)
	line = append(line, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.clients[conn]; ok {
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		_, _ = w.Write(line)
		_ = w.Flush()
	}
}

// writeTimeout bounds one write to one client; a local socket that takes
// longer has a reader that is gone or wedged.
const writeTimeout = 2 * time.Second
