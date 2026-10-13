// Package daemon is agenda's background process and the socket between it
// and the TUI. The daemon is agenda itself run headless: the same views,
// fetch loops, cache writes and notifications, with no screen. A TUI that
// finds one running paints from the cache the daemon keeps fresh, reloads
// a view when told that view's cache changed, and leaves fetching to it.
package daemon

import (
	"os"
	"path/filepath"
)

// Dir is where the socket, pid and log live: $XDG_STATE_HOME/agenda, or
// ~/.local/state/agenda. State, not cache: it describes a live process.
func Dir() (string, error) {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(d, "agenda"), nil
}

// SocketPath is the unix socket the daemon listens on.
func SocketPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "agenda.sock"), nil
}

// LogPath is where a spawned daemon writes its stderr.
func LogPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "daemon.log"), nil
}
