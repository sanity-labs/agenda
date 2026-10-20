package ui

import (
	"slices"

	"github.com/sanity-labs/agenda/internal/cache"
)

// Hideable is a list whose rows can be hidden by hand: a set of row keys,
// persisted per view, that wins over any filter when hiding is on.
type Hideable interface {
	LoadHidden(name string)
	SetHideHidden(on bool)
	ToggleHidden()
}

// LoadHidden reads the view's hidden set from the cache entry hidden-<name>.
func (l *List[T]) LoadHidden(name string) {
	l.hiddenName = "hidden-" + name
	l.hidden = map[string]bool{}
	ids, _ := cache.Load[[]string](l.hiddenName)
	for _, id := range ids {
		l.hidden[id] = true
	}
	l.applyFilter()
}

// SetHideHidden drops hidden rows from the list (on) or shows them marked.
func (l *List[T]) SetHideHidden(on bool) { l.hideHidden = on; l.applyFilter() }

// ToggleHidden hides the selected row, or unhides it when already hidden.
func (l *List[T]) ToggleHidden() {
	if l.hiddenName == "" || l.cursor < 0 || l.cursor >= len(l.filtered) {
		return
	}
	it := l.items[l.filtered[l.cursor]]
	if !selectable(it) {
		return
	}
	k := itemKey(it)
	if l.hidden[k] {
		delete(l.hidden, k)
	} else {
		l.hidden[k] = true
	}
	ids := make([]string, 0, len(l.hidden))
	for id := range l.hidden {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	_ = cache.Save(l.hiddenName, ids)
	l.applyFilter()
}

func (l *List[T]) isHidden(it T) bool { return l.hidden[itemKey(it)] }
