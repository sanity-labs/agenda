package ui

import "testing"

type keyedItem struct{ id, text string }

func (k keyedItem) Render(int, bool, Highlighter) string { return k.text }
func (k keyedItem) Fields() []Field                      { return []Field{{Name: "text", Text: k.text}} }
func (k keyedItem) Filter() string                       { return k.text }
func (k keyedItem) Key() string                          { return k.id }

// A refresh keeps the selection on the same item by identity, so a row
// whose text changed or that moved in the sort is still the one under the
// cursor; a row that is gone leaves the cursor where it was.
func TestSetItemsFollowsTheSelectedItemByKey(t *testing.T) {
	l := NewList[keyedItem]()
	l.SetSize(40, 10)
	l.SetItems([]keyedItem{{"a", "first"}, {"b", "second"}, {"c", "third"}})
	l.Select(func(k keyedItem) bool { return k.id == "b" })

	// Re-sorted and renamed: b is now last and reads differently.
	l.SetItems([]keyedItem{{"c", "third"}, {"a", "first"}, {"b", "second (approved)"}})
	if got := l.Selected().id; got != "b" {
		t.Errorf("after a reorder and rename the selection is %q, want b", got)
	}

	// b is gone: stay on the row that slid into its place, not the top.
	l.SetItems([]keyedItem{{"c", "third"}, {"a", "first"}})
	if got := l.Selected().id; got != "a" {
		t.Errorf("after the selected item vanished the cursor is on %q, want the row at its old index (a)", got)
	}
}

// A jump (Select) to a row the filter hides still lands: the row shows
// alongside the matches while it is selected, and the filter takes it
// back once the selection moves off it or the query changes.
func TestSelectShowsAHiddenRowWhileSelected(t *testing.T) {
	l := newTestList("apple", "banana", "cherry")
	l.SetQuery("ban")
	if l.Len() != 1 {
		t.Fatalf("filtered to %d rows, want 1", l.Len())
	}
	if !l.Select(func(s strItem) bool { return s == "cherry" }) {
		t.Fatal("a jump to a hidden row failed")
	}
	if l.Selected() != "cherry" || l.Len() != 2 {
		t.Fatalf("selected %q with %d rows, want cherry among 2", l.Selected(), l.Len())
	}
	l.move(-1)
	if l.Selected() != "banana" || l.Len() != 1 {
		t.Fatalf("after moving off: selected %q with %d rows, want banana alone", l.Selected(), l.Len())
	}
	if !l.Any(func(s strItem) bool { return s == "cherry" }) {
		t.Fatal("Any does not see a row the filter hides")
	}
	l.Select(func(s strItem) bool { return s == "cherry" })
	l.SetQuery("app")
	if l.Len() != 1 || l.Selected() != "apple" {
		t.Fatalf("a new query kept the pinned row: %q among %d", l.Selected(), l.Len())
	}
}
