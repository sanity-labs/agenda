package ui

// Messages between the root model and the views when a daemon is involved.
// The root model owns the socket; the views only see these.

// CacheChangedMsg says the daemon rewrote a cache entry; the view that
// owns that name reloads from it rather than fetching.
type CacheChangedMsg struct{ Name string }

// SeenMsg reaches the daemon's views: rows a client selected, whose
// unread marks must go before the next cache save would revive them.
// View is the lowercased tab title.
type SeenMsg struct {
	View string
	IDs  []string
}
