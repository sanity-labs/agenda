package notify

import (
	"os"
	"testing"
)

// TestMain keeps every test away from the real config, cache and state
// dirs. A settings test once toggled "mark new items" in the developer's
// own config.yml on every run, since it wrote wherever XDG_CONFIG_HOME
// pointed, which was nowhere.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agenda-test-")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		if err := os.Setenv(k, dir); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
