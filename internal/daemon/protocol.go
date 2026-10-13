package daemon

import (
	"encoding/json"
	"time"
)

// Protocol is bumped when a message shape changes; a client and daemon
// built from different versions compare it in status rather than
// guessing from behaviour.
const Protocol = 1

// Request is one call over the socket: an id the response echoes, a
// method, and its parameters. One JSON object per line, both ways.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers a request by id with a result or an error, never both.
type Response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a failed call's reason, with a stable code for scripts.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Event is a push from the daemon to every connected client: what
// changed, not a reply to anything.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Methods a client may call.
const (
	MethodStatus  = "status"  // -> Status
	MethodRefresh = "refresh" // RefreshParams; the daemon refetches
	MethodSeen    = "seen"    // SeenParams; rows the user looked at
	MethodStop    = "stop"    // the daemon quits after answering
)

// Events the daemon pushes.
const (
	EventCacheChanged = "cache_changed" // CacheChanged: a view's cache was rewritten
	EventToast        = "toast"         // Toast: a popup the daemon would have shown
	EventStatus       = "status"        // StatusLine: a message-log entry
	EventStopping     = "stopping"      // the daemon is going away
)

// Status is what `agenda status` and the TUI's footer read.
type Status struct {
	Version  string    `json:"version"`
	Protocol int       `json:"protocol"`
	PID      int       `json:"pid"`
	Started  time.Time `json:"started"`
	Views    []string  `json:"views"`
	Clients  int       `json:"clients"`
}

// RefreshParams names the view to refetch, by its lowercased title; empty
// refetches every view.
type RefreshParams struct {
	View string `json:"view,omitempty"`
}

// SeenParams are rows the user selected in a client, so the daemon drops
// their unread marks rather than reviving them on its next save.
type SeenParams struct {
	View string   `json:"view"`
	IDs  []string `json:"ids"`
}

// CacheChanged names the cache file (without .json) a view rewrote.
type CacheChanged struct {
	Name string `json:"name"`
}

// Toast is a terminal popup raised in the daemon, for the client to show.
type Toast struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Success bool   `json:"success,omitempty"`
}

// StatusLine is a message-log entry raised in the daemon.
type StatusLine struct {
	Severity int       `json:"severity"`
	Source   string    `json:"source"`
	Summary  string    `json:"summary"`
	Detail   string    `json:"detail,omitempty"`
	At       time.Time `json:"at"`
}

// Decode reads a params or data blob into v; empty means the zero value.
func Decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// marshal is json.Marshal for values that cannot fail.
func marshal(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
