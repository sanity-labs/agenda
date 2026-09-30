package linear

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

// The settings overlay offers config's list, but this view resolves the
// names. If the two drift, a listed sort silently falls back to the
// default and the setting looks broken.
func TestConfigSortNamesMatchThisView(t *testing.T) {
	mine := SortNames()
	listed := configList()
	if len(mine) != len(listed) {
		t.Fatalf("view has %v, config offers %v", mine, listed)
	}
	for i := range mine {
		if mine[i] != listed[i] {
			t.Errorf("index %d: view %q, config %q (full: %v vs %v)",
				i, mine[i], listed[i], mine, listed)
		}
	}
	// Every listed name must resolve, not just fall back.
	for _, name := range listed {
		if _, ok := sortByName(name); !ok {
			t.Errorf("config offers %q but the view does not know it", name)
		}
	}
}

// An unknown name is a typo, not a crash: the view opens on its default.
func TestUnknownSortFallsBack(t *testing.T) {
	if _, ok := sortByName("nonsense"); ok {
		t.Error("an unknown sort name reported as known")
	}
	if mode, _ := sortByName("nonsense"); mode != sortRecent {
		t.Errorf("fallback = %v, want the default", mode)
	}
}

func configList() []string { return config.LinearSortNames }
