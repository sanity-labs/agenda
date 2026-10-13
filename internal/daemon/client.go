package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// Client is one connection to the daemon: calls get answers by id, and
// pushes arrive on Events until the connection drops.
type Client struct {
	conn    net.Conn
	w       *bufio.Writer
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Response
	next    int
	events  chan Event
	closed  chan struct{}
	once    sync.Once
}

// ErrClosed is returned by Call once the connection is gone.
var ErrClosed = errors.New("daemon connection closed")

// Dial connects to the socket at path. A missing or dead socket fails
// fast; nothing is spawned here.
func Dial(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		w:       bufio.NewWriter(conn),
		pending: map[string]chan Response{},
		events:  make(chan Event, 256), // a greeting can be ~70 events; losing the last would lose the summary toast
		closed:  make(chan struct{}),
	}
	go c.read()
	return c, nil
}

// Events delivers the daemon's pushes; it closes when the connection does.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when the connection is gone, for callers that only wait.
func (c *Client) Done() <-chan struct{} { return c.closed }

// Call sends a request and waits for its answer, decoding the result into
// out when out is not nil.
func (c *Client) Call(method string, params any, out any) error {
	c.mu.Lock()
	c.next++
	id := strconv.Itoa(c.next)
	ch := make(chan Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := Request{ID: id, Method: method}
	if params != nil {
		req.Params = marshal(params)
	}
	line, _ := json.Marshal(req)
	c.wmu.Lock()
	_, err := c.w.Write(append(line, '\n'))
	if err == nil {
		err = c.w.Flush()
	}
	c.wmu.Unlock()
	if err != nil {
		c.drop(id)
		return err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-c.closed:
		return ErrClosed
	case <-time.After(10 * time.Second):
		c.drop(id)
		return errors.New("daemon did not answer " + method)
	}
}

// Close ends the connection; Events and Done close with it.
func (c *Client) Close() {
	c.once.Do(func() {
		_ = c.conn.Close()
		close(c.closed)
	})
}

func (c *Client) drop(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// read demuxes the stream: a line with an id answers a call, a line with
// an event is pushed to Events.
func (c *Client) read() {
	defer func() {
		c.Close()
		close(c.events)
	}()
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var probe struct {
			ID    string `json:"id"`
			Event string `json:"event"`
		}
		if json.Unmarshal(sc.Bytes(), &probe) != nil {
			continue
		}
		if probe.Event != "" {
			var ev Event
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				select {
				case c.events <- ev:
				default: // a client that stopped reading loses pushes, not calls
				}
			}
			continue
		}
		var resp Response
		if json.Unmarshal(sc.Bytes(), &resp) != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}
