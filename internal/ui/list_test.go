package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// strItem is a trivial Item with a single "text" field equal to the string.
type strItem string

func (s strItem) Render(width int, selected bool, hl Highlighter) string { return string(s) }
func (s strItem) Fields() []Field                                        { return []Field{{Name: "text", Text: string(s)}} }
func (s strItem) Filter() string                                         { return string(s) }

// fieldItem is a multi-field Item for scoped-filter tests.
type fieldItem struct{ repo, title string }

func (f fieldItem) Render(width int, selected bool, hl Highlighter) string {
	return f.repo + " " + f.title
}
func (f fieldItem) Fields() []Field {
	return []Field{{Name: "repo", Text: f.repo}, {Name: "title", Text: f.title}}
}
func (f fieldItem) Filter() string { return f.repo + " " + f.title }

// proseItem has a fuzzy title field and a substring-only prose body field.
type proseItem struct{ title, body string }

func (p proseItem) Render(width int, selected bool, hl Highlighter) string { return p.title }
func (p proseItem) Fields() []Field {
	return []Field{
		{Name: "title", Text: p.title},
		{Name: "text", Text: p.body, Prose: true},
	}
}
func (p proseItem) Filter() string { return p.title }

func TestProseFieldMatchesSubstringNotSubsequence(t *testing.T) {
	l := NewList[proseItem]()
	l.SetItems([]proseItem{
		{title: "alpha", body: "the word MANDATORY appears here"},
		{title: "beta", body: "many random abstract nouns deliver text"}, // has M..A..N..D..A..T as subsequence
	})
	// Scope to the prose field only, then search a term that is a subsequence of
	// the second body but a literal substring of neither-except the first.
	l.SetEnabledFields([]string{"text"})
	l.SetQuery("mandat")
	if l.Len() != 1 {
		t.Fatalf("prose search matched %d items, want 1 (substring only, not subsequence)", l.Len())
	}
	if l.Selected().title != "alpha" {
		t.Errorf("matched %q, want alpha (the one with literal MANDAT)", l.Selected().title)
	}
}

func TestTitleHighlightSuppressedWhenTitleNotScoped(t *testing.T) {
	l := NewList[proseItem]()
	l.SetItems([]proseItem{{title: "mandate report", body: "x"}})
	l.SetEnabledFields([]string{"text"}) // title NOT in scope
	l.SetQuery("mandat")
	// highlighter() must be inert so the title isn't fuzzy-highlighted.
	if hl := l.highlighter(); hl.Query != "" {
		t.Errorf("highlighter active for title while title unscoped: Query=%q", hl.Query)
	}
	l.SetEnabledFields([]string{"title", "text"}) // title back in scope
	if hl := l.highlighter(); hl.Query == "" {
		t.Errorf("highlighter should be active when title is scoped")
	}
}

func newTestList(items ...string) List[strItem] {
	l := NewList[strItem]()
	conv := make([]strItem, len(items))
	for i, s := range items {
		conv[i] = strItem(s)
	}
	l.SetItems(conv)
	return l
}

func TestListSelectionDefaults(t *testing.T) {
	l := newTestList("a", "b", "c")
	if l.Total() != 3 || l.Len() != 3 {
		t.Fatalf("Total=%d Len=%d, want 3/3", l.Total(), l.Len())
	}
	if got := l.Selected(); got != "a" {
		t.Errorf("Selected() = %q, want first item %q", got, "a")
	}

	empty := NewList[strItem]()
	if got := empty.Selected(); got != "" {
		t.Errorf("empty Selected() = %q, want zero value", got)
	}
}

func TestListSelectionPreservedAcrossSetItems(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.cursor = 2 // select "c"
	if l.Selected() != "c" {
		t.Fatalf("precondition: Selected()=%q", l.Selected())
	}
	// Replace with a reordered set that still contains "c".
	l.SetItems([]strItem{"c", "a", "b"})
	if got := l.Selected(); got != "c" {
		t.Errorf("after SetItems Selected() = %q, want %q (preserved by identity)", got, "c")
	}
}

func TestListSelectionResetsWhenItemGone(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.cursor = 2
	l.SetItems([]strItem{"x", "y"}) // "c" no longer present
	if got := l.Selected(); got != "x" {
		t.Errorf("Selected() = %q, want clamped to first %q", got, "x")
	}
}

func TestListScrollBy(t *testing.T) {
	l := newTestList("a", "b", "c", "d", "e")
	if l.Selected() != "a" {
		t.Fatalf("precondition: Selected()=%q, want a", l.Selected())
	}
	l.ScrollBy(3)
	if l.Selected() != "d" {
		t.Errorf("after ScrollBy(3) Selected()=%q, want d", l.Selected())
	}
	l.ScrollBy(100) // clamps to last
	if l.Selected() != "e" {
		t.Errorf("after ScrollBy(100) Selected()=%q, want e (clamped)", l.Selected())
	}
	l.ScrollBy(-100) // clamps to first
	if l.Selected() != "a" {
		t.Errorf("after ScrollBy(-100) Selected()=%q, want a (clamped)", l.Selected())
	}
}

func TestListFilterSubsequence(t *testing.T) {
	l := newTestList("apple", "apricot", "banana")
	l.query = "ap"
	l.applyFilter()
	if l.Len() != 2 {
		t.Errorf("Len() = %d, want 2 (apple, apricot)", l.Len())
	}
	l.query = "ban"
	l.applyFilter()
	if l.Len() != 1 || l.Selected() != "banana" {
		t.Errorf("Len=%d Selected=%q, want 1/banana", l.Len(), l.Selected())
	}
	if l.Total() != 3 {
		t.Errorf("Total() = %d, want 3 (unfiltered count)", l.Total())
	}
}

func TestFilterAcceptsSpace(t *testing.T) {
	l := newTestList("alpha beta", "gamma", "alpha gamma")
	l.SetSize(40, 10)
	l.Update(tea.KeyPressMsg{Code: '/', Text: "/"}) // begin filtering
	if !l.Filtering() {
		t.Fatal("expected filtering after '/'")
	}
	for _, r := range "alpha beta" { // includes a space
		l.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if l.Query() != "alpha beta" {
		t.Errorf("query = %q, want %q (space must be accepted)", l.Query(), "alpha beta")
	}
	if l.Len() != 1 || l.Selected() != "alpha beta" {
		t.Errorf("filtered = %d / %q, want 1 / alpha beta", l.Len(), l.Selected())
	}
}

func TestNavigateWhileFiltering(t *testing.T) {
	l := newTestList("apple", "apricot", "avocado")
	l.SetSize(40, 10)
	l.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	l.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}) // matches all three
	if l.Selected() != "apple" {
		t.Fatalf("Selected = %q, want apple", l.Selected())
	}
	l.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // arrow navigates, still filtering
	if !l.Filtering() {
		t.Error("arrow should not exit filtering")
	}
	if l.Selected() != "apricot" {
		t.Errorf("after down Selected = %q, want apricot", l.Selected())
	}
}

func TestMatchesSubsequence(t *testing.T) {
	cases := []struct {
		s, q string
		want bool
	}{
		{"banana", "ban", true},
		{"banana", "bnn", true}, // subsequence, non-contiguous
		{"banana", "xyz", false},
		{"banana", "", true},   // empty query matches anything
		{"abc", "abcd", false}, // query longer than string
	}
	for _, c := range cases {
		if got := matchesSubsequence(c.s, c.q); got != c.want {
			t.Errorf("matchesSubsequence(%q, %q) = %v, want %v", c.s, c.q, got, c.want)
		}
	}
}

func TestVisibleItemsWithRowHeight(t *testing.T) {
	l := newTestList("a", "b", "c", "d", "e", "f")
	l.SetSize(40, 10)
	if got := l.visibleItems(); got != 10 {
		t.Errorf("visibleItems() with rowHeight 1, height 10 = %d, want 10", got)
	}
	l.SetRowHeight(2)
	if got := l.visibleItems(); got != 5 {
		t.Errorf("visibleItems() with rowHeight 2, height 10 = %d, want 5", got)
	}
}

func TestScrollbar(t *testing.T) {
	hasThumb := func(s string) bool { return strings.Contains(s, "┃") }

	// Everything fits: no bar (all blank cells).
	for _, c := range Scrollbar(5, 3, 5, 0) {
		if strings.TrimSpace(c) != "" {
			t.Fatalf("no-overflow cell = %q, want blank", c)
		}
	}
	// At the top, the thumb is at the top and not the bottom.
	top := Scrollbar(10, 100, 10, 0)
	if !hasThumb(top[0]) || hasThumb(top[9]) {
		t.Errorf("offset 0: thumb should be at the top, not the bottom")
	}
	// At the bottom (max offset = total-visible), the thumb is at the bottom.
	bot := Scrollbar(10, 100, 10, 90)
	if !hasThumb(bot[9]) || hasThumb(bot[0]) {
		t.Errorf("max offset: thumb should be at the bottom, not the top")
	}
}

func TestClampCursorWindow(t *testing.T) {
	l := newTestList("0", "1", "2", "3", "4", "5", "6", "7", "8", "9")
	l.SetSize(40, 3) // window of 3 items
	l.cursor = 5
	l.clampCursor()
	if l.offset != 3 {
		t.Errorf("offset = %d, want 3 (cursor 5 - window 3 + 1)", l.offset)
	}
	// Cursor above the window pulls the offset up.
	l.cursor = 1
	l.clampCursor()
	if l.offset != 1 {
		t.Errorf("offset = %d, want 1 (cursor scrolled above window)", l.offset)
	}
}

func TestListScopedFieldMatching(t *testing.T) {
	l := NewList[fieldItem]()
	l.SetItems([]fieldItem{
		{repo: "agenda", title: "add oauth"},
		{repo: "oauth-lib", title: "bump deps"},
	})

	// All fields on: "oauth" matches both (row 1 via title, row 2 via repo).
	l.SetQuery("oauth")
	if l.Len() != 2 {
		t.Fatalf("all-fields: Len=%d, want 2", l.Len())
	}

	// Scope to repo only: "oauth" matches only the repo "oauth-lib".
	l.SetEnabledFields([]string{"repo"})
	if l.Len() != 1 {
		t.Fatalf("repo-only: Len=%d, want 1", l.Len())
	}
	if got := l.Selected(); got.repo != "oauth-lib" {
		t.Errorf("repo-only Selected repo=%q, want oauth-lib", got.repo)
	}

	// Scope to title only: "oauth" matches only "add oauth".
	l.SetEnabledFields([]string{"title"})
	if l.Len() != 1 || l.Selected().title != "add oauth" {
		t.Errorf("title-only Len=%d Selected.title=%q, want 1/add oauth", l.Len(), l.Selected().title)
	}

	// Empty enabled set means all-on again.
	l.SetEnabledFields(nil)
	if l.Len() != 2 {
		t.Errorf("nil-enabled: Len=%d, want 2 (all on)", l.Len())
	}
}

func TestListCaseSensitivity(t *testing.T) {
	l := NewList[fieldItem]()
	l.SetItems([]fieldItem{{repo: "Agenda", title: "Foo"}})

	l.SetQuery("agenda")
	if l.Len() != 1 {
		t.Fatalf("case-insensitive: Len=%d, want 1", l.Len())
	}
	l.SetCaseSensitive(true)
	if l.Len() != 0 {
		t.Errorf("case-sensitive 'agenda' vs 'Agenda': Len=%d, want 0", l.Len())
	}
	if !l.CaseSensitive() {
		t.Errorf("CaseSensitive() = false, want true")
	}
}

func TestListFieldNames(t *testing.T) {
	l := NewList[fieldItem]()
	l.SetItems([]fieldItem{{repo: "a", title: "b"}})
	got := l.FieldNames()
	if len(got) != 2 || got[0] != "repo" || got[1] != "title" {
		t.Errorf("FieldNames() = %v, want [repo title]", got)
	}
}

func TestListEnabledFields(t *testing.T) {
	l := NewList[fieldItem]()
	l.SetItems([]fieldItem{{repo: "a", title: "b"}})

	// All on by default → empty (all).
	if got := l.EnabledFields(); len(got) != 0 {
		t.Errorf("default EnabledFields() = %v, want empty (all on)", got)
	}

	// Scope to a subset → names in declaration order (repo before title),
	// regardless of the order passed in.
	l.SetEnabledFields([]string{"title", "repo"})
	got := l.EnabledFields()
	if len(got) != 2 || got[0] != "repo" || got[1] != "title" {
		t.Errorf("EnabledFields() = %v, want [repo title] in declaration order", got)
	}

	// Back to all → empty again.
	l.SetEnabledFields(nil)
	if got := l.EnabledFields(); len(got) != 0 {
		t.Errorf("EnabledFields() after nil = %v, want empty (all on)", got)
	}
}

func TestFilterLine(t *testing.T) {
	l := NewList[fieldItem]()
	l.SetItems([]fieldItem{
		{repo: "agenda", title: "add oauth"},
		{repo: "oauth-lib", title: "bump"},
	})

	// No filter, not filtering: empty line.
	if got := l.FilterLine(); got != "" {
		t.Errorf("idle FilterLine = %q, want empty", got)
	}

	// Scoped query shows the field names, the query, and N/Total.
	l.SetEnabledFields([]string{"repo"})
	l.SetQuery("oauth")
	got := l.FilterLine()
	for _, want := range []string{"repo", "oauth", "1/2"} {
		if !strings.Contains(got, want) {
			t.Errorf("FilterLine = %q, want it to contain %q", got, want)
		}
	}

	// Case-sensitive adds an "Aa" marker.
	l.SetCaseSensitive(true)
	if got := l.FilterLine(); !strings.Contains(got, "Aa") {
		t.Errorf("case-sensitive FilterLine = %q, want it to contain Aa", got)
	}
}

func TestListClickAt(t *testing.T) {
	l := NewList[sepItem]()
	l.SetItems([]sepItem{
		{text: "a\n."},
		{text: "lane\n.", sep: true},
		{text: "b\n."},
		{text: "c\n."},
		{text: "d\n."},
	})
	l.SetRowHeight(2)
	l.SetSize(40, 7) // three two-line rows fit; line 6 is padding

	if !l.ClickAt(1) || l.Selected().text != "a\n." {
		t.Errorf("click on row 0's second line selected %q, want a", l.Selected().text)
	}
	if !l.ClickAt(4) || l.Selected().text != "b\n." {
		t.Errorf("click on line 4 selected %q, want b (row 2)", l.Selected().text)
	}
	for _, y := range []int{2, 6, -1} {
		if l.ClickAt(y) {
			t.Errorf("ClickAt(%d) hit; want a miss (separator / below window / above list)", y)
		}
		if l.Selected().text != "b\n." {
			t.Errorf("ClickAt(%d) moved the cursor to %q", y, l.Selected().text)
		}
	}

	// Scrolled down, line 0 is the first visible item, not the first item.
	l.ScrollBy(10)
	if !l.ClickAt(0) || l.Selected().text != "b\n." {
		t.Errorf("click on line 0 after scrolling selected %q, want b (offset 2)", l.Selected().text)
	}
}

func TestListClickBelowLastItemMisses(t *testing.T) {
	l := newTestList("a", "b")
	l.SetSize(40, 10)
	if l.ClickAt(5) {
		t.Error("click in the empty space under a short list hit an item")
	}
}
