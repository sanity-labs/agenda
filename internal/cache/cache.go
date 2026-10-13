// Package cache is a tiny, generic on-disk cache for view data, used to paint
// the UI instantly from the last run while the live data refreshes in the
// background (stale-while-revalidate). Each entry is a JSON blob under
// $XDG_CACHE_HOME/agenda (regenerable cache, never config or secrets), written
// atomically so a crash or a second instance can't corrupt it.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Dir is the cache directory: $XDG_CACHE_HOME/agenda, or ~/.cache/agenda.
func Dir() (string, error) {
	d := os.Getenv("XDG_CACHE_HOME")
	if d == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(home, ".cache")
	}
	return filepath.Join(d, "agenda"), nil
}

func path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name+".json"), nil
}

// Load reads <name>.json into a value of type T. A missing or unreadable file
// yields the zero value and ok=false — never an error, since the cache is
// always optional.
func Load[T any](name string) (T, bool) {
	var v T
	p, err := path(name)
	if err != nil {
		return v, false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return v, false
	}
	if json.Unmarshal(raw, &v) != nil {
		return v, false
	}
	return v, true
}

// OnSave, when set, is told the name of every entry written. The daemon
// uses it to tell its clients which view to reload; nothing else should.
var OnSave func(name string)

// Save writes v to <name>.json atomically (temp file + rename). Errors are
// returned but are safe to ignore — a failed cache write only costs a slower
// next start.
func Save[T any](name string, v T) error {
	err := save(name, v)
	if err == nil && OnSave != nil {
		OnSave(name)
	}
	return err
}

func save[T any](name string, v T) error {
	p, err := path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), name+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}
