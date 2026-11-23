package prs

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

// The remembered sort belongs to your own list, not the temporary repo list.
func TestSortInRepoListIsNotRemembered(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.repoName = "acme/agenda"
	v.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	v.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	if v.sort == sortRecent && !v.rev {
		t.Fatal("sort keys did nothing in the repo list")
	}
	w := New(config.GitHubConfig{}, nil, nil, nil)
	w.RestoreSort()
	if w.sort != sortRecent || w.rev {
		t.Errorf("repo-list sort leaked into the remembered one: %v rev=%v", w.sort, w.rev)
	}
}
