package linear

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

func sortPress(v *View, r rune) { v.Update(tea.KeyPressMsg{Code: r, Text: string(r)}) }

func TestSortIsRememberedPerSource(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.LinearConfig{Token: "t", Triage: true, Sort: "priority"}

	first := New(cfg, nil, nil, nil)
	sortPress(first, 's') // priority -> date
	sortPress(first, 'S')
	first.useSource(inboxSource)
	if first.sort != sortPriority || first.rev {
		t.Errorf("a source with no remembered sort got %v rev=%v, want the config default", first.sort, first.rev)
	}
	sortPress(first, 's')
	first.useSource(first.defaultSource)
	if first.sort != sortRecent || !first.rev {
		t.Errorf("default source sort = %v rev=%v, want date reversed", first.sort, first.rev)
	}

	second := New(cfg, nil, nil, nil)
	second.RestoreSource() // ends on the default source
	if second.sort != sortRecent || !second.rev {
		t.Errorf("restored %v rev=%v, want date reversed", second.sort, second.rev)
	}
	second.useSource(inboxSource)
	if second.sort != sortRecent || second.rev {
		t.Errorf("inbox restored %v rev=%v, want date", second.sort, second.rev)
	}
}

func TestFavouritesPaintFromCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.LinearConfig{Token: "t"}

	first := New(cfg, nil, nil, nil)
	first.Update(favsMsg{projects: []navProject{{ID: "p1", Name: "Infra", Kind: "project"}}})
	first.useSource(navSource{Kind: "project", ProjectID: "p1", Label: "Infra"})

	second := New(cfg, nil, nil, nil)
	second.RestoreSource()
	if len(second.favs) != 1 || second.favs[0].Kind != "project" {
		t.Fatalf("favs = %+v, want the cached project", second.favs)
	}
	if second.restoring || second.source.ProjectID != "p1" {
		t.Errorf("restoring=%v source=%+v, want the project source resolved from cache", second.restoring, second.source)
	}
}
