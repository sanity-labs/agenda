package prs

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

func TestSortIsRememberedAcrossRestarts(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first := New(config.GitHubConfig{}, nil, nil, nil)
	first.Update(press('s'))
	first.Update(press('S'))

	second := New(config.GitHubConfig{}, nil, nil, nil)
	second.RestoreSort()
	if second.sort != sortReview || !second.rev {
		t.Errorf("sort = %v rev = %v, want review reversed", second.sort, second.rev)
	}

	reviews := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	reviews.RestoreSort()
	if reviews.sort != sortRecent || reviews.rev {
		t.Error("the Reviews tab picked up the PRs tab's sort")
	}
}
