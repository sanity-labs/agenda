package prs

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

// The configured sort is what the view opens on: it used to revert to the
// default on every start, with no way to set it.
func TestConfiguredSortIsUsedAtStartup(t *testing.T) {
	for _, name := range config.PRSortNames {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		v := New(config.GitHubConfig{Sort: name}, nil, nil, nil)
		if got := sortName[v.sort]; got != name {
			t.Errorf("sort = %q, want the configured %q", got, name)
		}
	}
}

// Empty means the view's own default, so an unset key changes nothing.
func TestUnsetSortKeepsTheDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	if v.sort != sortRecent {
		t.Errorf("sort = %v with no config, want the default", v.sort)
	}
	if v.rev {
		t.Error("reverse defaulted on")
	}
}

// A typo opens the view on its default rather than failing.
func TestBadSortNameFallsBackAtStartup(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{Sort: "nonsense"}, nil, nil, nil)
	if v.sort != sortRecent {
		t.Errorf("sort = %v for an unknown name, want the default", v.sort)
	}
}

// Reverse is part of the startup state, not just a runtime key.
func TestConfiguredReverseIsUsedAtStartup(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{Sort: "repo", Reverse: true}, nil, nil, nil)
	if !v.rev {
		t.Error("reverse was not applied from config")
	}
	if sortName[v.sort] != "repo" {
		t.Errorf("sort = %q, want repo", sortName[v.sort])
	}
}
