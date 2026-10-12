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
