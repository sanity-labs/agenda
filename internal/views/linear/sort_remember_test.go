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
