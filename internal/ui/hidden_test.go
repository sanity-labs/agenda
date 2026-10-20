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

// A jump to a hidden row lands on it with hiding on, and it drops out again
// once the selection moves off.
func TestJumpToHiddenRow(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	l := NewList[keyedItem]()
	l.SetSize(40, 10)
	l.LoadHidden("test")
	l.SetItems([]keyedItem{{"a", "apple"}, {"b", "banana"}})
	l.ToggleHidden()
	l.SetHideHidden(true)
	if l.SelectedHidden() {
		t.Fatal("no hidden row is selected yet")
	}

	if !l.Select(func(k keyedItem) bool { return k.id == "a" }) || l.Selected().id != "a" {
		t.Fatal("jump did not land on the hidden row")
	}
	if !l.SelectedHidden() {
		t.Fatal("SelectedHidden false on a jumped-to hidden row")
	}

	l.Select(func(k keyedItem) bool { return k.id == "b" })
	l.unpinIfLeft()
	if l.Len() != 1 {
		t.Fatalf("%d rows after leaving the hidden one; want 1", l.Len())
	}
}
