package ui

// Messages between the root model and the views when a daemon is involved.
// The root model owns the socket; the views only see these.

// CacheChangedMsg says the daemon rewrote a cache entry; the view that
// owns that name reloads from it rather than fetching. CacheAll names
// every entry: a client that just reconnected repaints everything.
type CacheChangedMsg struct{ Name string }

// CacheAll is the Name that matches every view's cache.
const CacheAll = "*"

// Matches reports whether the message is about the named cache entry.
func (m CacheChangedMsg) Matches(name string) bool { return m.Name == CacheAll || m.Name == name }

// SelectedMsg tells a view the root model selected a row in it (a jump
// from another view), so it can react the way it does to its own moves.
type SelectedMsg struct{}

// SeenMsg reaches the daemon's views: rows a client selected, whose
// unread marks must go before the next cache save would revive them.
// View is the lowercased tab title.
type SeenMsg struct {
	View string
	IDs  []string
}
