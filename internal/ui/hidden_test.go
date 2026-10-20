package ui

import (
	"strings"
	"testing"
)

// i hides the selected row and i again unhides it; shown, a hidden row is
// marked, and with hiding on it stays out whatever the filter matches.
func TestToggleHidden(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	items := []keyedItem{{"a", "apple"}, {"b", "banana"}}
	l := NewList[keyedItem]()
	l.SetSize(40, 10)
	l.LoadHidden("test")
	l.SetItems(items)
	l.ToggleHidden()

	reloaded := NewList[keyedItem]()
	reloaded.LoadHidden("test")
	reloaded.SetItems(items)
	if !reloaded.hidden["a"] {
		t.Fatal("hidden set did not survive a reload")
	}
	if !strings.Contains(l.View(), IconHidden) {
		t.Fatal("a shown hidden row has no mark")
	}

	l.SetQuery("apple")
	l.SetHideHidden(true)
	if l.Len() != 0 {
		t.Fatalf("with hiding on, %d rows match; want the hidden one dropped despite the filter", l.Len())
	}

	l.SetHideHidden(false)
	l.SetQuery("")
	l.Select(func(k keyedItem) bool { return k.id == "a" })
	l.ToggleHidden()
	if l.hidden["a"] || strings.Contains(l.View(), IconHidden) {
		t.Fatal("i again did not unhide")
	}
}
