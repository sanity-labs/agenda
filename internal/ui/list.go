// Package ui holds reusable widgets shared by every view: a generic,
// fuzzy-filterable selectable list, plus small rendering helpers. Keeping
// these here (rather than in tui) lets view packages depend on them without
// depending on the root model.
package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Field is one named, scopable piece of an Item's searchable text. Prose marks
// a large free-text field (e.g. a conversation body) that should be matched by
// literal substring rather than fuzzy subsequence — subsequence matching on
// long prose matches almost any query.
type Field struct {
	Name  string
	Text  string
	Prose bool
}

// Item is anything a List can hold. Render returns the row text for the given
// width, selection state, and active highlighter. Fields returns the named
// fields the scoped filter can target; Filter returns all field text joined,
// for the quick all-fields filter.
type Item interface {
	Render(width int, selected bool, hl Highlighter) string
	Fields() []Field
	Filter() string
}

// NonSelectable is optionally implemented by items that render but can never
// hold the cursor: section separators and group headers. They are also
// dropped whenever a filter query is active, so filtered lists stay flat.
type NonSelectable interface {
	Selectable() bool
}

func selectable(it any) bool {
	ns, ok := it.(NonSelectable)
	return !ok || ns.Selectable()
}

// List is a vertically-scrolling, filterable, single-selection list. It is
// generic over the concrete item type so views get type-safe Selected().
type List[T Item] struct {
	items    []T
	filtered []int // indices into items that match the current filter
	cursor   int   // index into filtered
	offset   int   // first visible row (index into filtered)

	width, height int
	rowHeight     int // lines per item (default 1); set higher for multi-line rows

	filtering bool
	query     string

	enabled       map[string]bool // field name -> on; nil/empty = all on
	caseSensitive bool

	keys listKeys
}

type listKeys struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, Filter, Clear key.Binding
}

func defaultListKeys() listKeys {
	return listKeys{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Top:      key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:   key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		HalfUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ page up")),
		HalfDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ page down")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Clear:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
	}
}

func NewList[T Item]() List[T] {
	return List[T]{keys: defaultListKeys(), rowHeight: 1}
}

// Rebind resolves the list's navigation keys through a KeyResolver, letting
// config override them (scope "list" by convention; the resolver closes over
// the scope). Help text stays with the defaults; the footer never shows
// list-navigation bindings.
func (l *List[T]) Rebind(of KeyResolver) {
	bind := func(action string, def ...string) key.Binding {
		return Bind(of(action, def...), "", "")
	}
	l.keys = listKeys{
		Up:       bind("up", "up", "k"),
		Down:     bind("down", "down", "j"),
		Top:      bind("top", "g", "home"),
		Bottom:   bind("bottom", "G", "end"),
		HalfUp:   bind("half_up", "ctrl+u"),
		HalfDown: bind("half_down", "ctrl+d"),
		Filter:   bind("quick_filter", "/"),
		Clear:    bind("clear_filter", "esc"),
	}
}

// SetRowHeight declares how many lines each item's Render produces, so the
// list's scrolling and windowing account for multi-line rows. Items must
// render exactly this many lines.
func (l *List[T]) SetRowHeight(h int) {
	l.rowHeight = max(h, 1)
	l.clampCursor()
}

// visibleItems is how many items fit in the current height.
func (l *List[T]) visibleItems() int {
	rh := max(l.rowHeight, 1)
	return max(1, l.height/rh)
}

// SetItems replaces the contents, preserving the selected item by identity of
// its Filter() value where possible.
func (l *List[T]) SetItems(items []T) {
	prev := l.Selected()
	var prevKey string
	var hadPrev bool
	if any(prev) != nil {
		prevKey = prev.Filter()
		hadPrev = true
	}

	l.items = items
	l.applyFilter()

	if hadPrev {
		found := false
		for i, idx := range l.filtered {
			if l.items[idx].Filter() == prevKey {
				l.cursor = i
				found = true
				break
			}
		}
		// If the previously-selected item is gone, jump to the top rather than
		// leaving the cursor on whatever now sits at the old index.
		if !found {
			l.cursor = 0
		}
	}
	l.clampCursor()
}

func (l *List[T]) SetSize(w, h int) { l.width, l.height = w, h; l.clampCursor() }

// Filtering reports whether the list is currently capturing filter input.
func (l *List[T]) Filtering() bool { return l.filtering }

// Query is the active filter string.
func (l *List[T]) Query() string { return l.query }

// SetQuery sets the filter query and re-filters. Empty query clears the match.
func (l *List[T]) SetQuery(q string) { l.query = q; l.applyFilter() }

// SetEnabledFields scopes matching to the named fields. nil/empty enables all.
func (l *List[T]) SetEnabledFields(names []string) {
	if len(names) == 0 {
		l.enabled = nil
	} else {
		l.enabled = make(map[string]bool, len(names))
		for _, n := range names {
			l.enabled[n] = true
		}
	}
	l.applyFilter()
}

// SetCaseSensitive toggles case-sensitive matching and re-filters.
func (l *List[T]) SetCaseSensitive(b bool) { l.caseSensitive = b; l.applyFilter() }

// CaseSensitive reports the current case-sensitivity setting.
func (l *List[T]) CaseSensitive() bool { return l.caseSensitive }

// FieldNames returns every field name the items expose, in declaration order,
// read from the first real item; group headers have no fields, so a grouped
// list must not consult its leading header. Empty if there are no real items.
func (l *List[T]) FieldNames() []string {
	for i := range l.items {
		if !selectable(l.items[i]) {
			continue
		}
		fields := l.items[i].Fields()
		names := make([]string, len(fields))
		for j, f := range fields {
			names[j] = f.Name
		}
		return names
	}
	return nil
}

// EnabledFields returns the field names currently enabled, in declaration
// order. Empty slice means all fields are enabled.
func (l *List[T]) EnabledFields() []string {
	if len(l.enabled) == 0 {
		return nil
	}
	var out []string
	for _, n := range l.FieldNames() {
		if l.enabled[n] {
			out = append(out, n)
		}
	}
	return out
}

// fieldEnabled reports whether a field participates in matching.
func (l *List[T]) fieldEnabled(name string) bool {
	return len(l.enabled) == 0 || l.enabled[name]
}

// highlighter is the active Highlighter for in-row title highlighting. It is
// suppressed unless the "title" field participates in matching — otherwise a
// query aimed at another field (e.g. the conversation body) would fuzzy-match
// and highlight stray characters in the title. When there's no explicit scope
// (all fields on), the title is a match target and highlighting applies.
func (l *List[T]) highlighter() Highlighter {
	if !l.fieldEnabled("title") {
		return Highlighter{}
	}
	return Highlighter{Query: strings.TrimSpace(l.query), CaseSensitive: l.caseSensitive}
}

// Len is the number of items after filtering.
func (l *List[T]) Len() int { return len(l.filtered) }

// Total is the number of items before filtering.
func (l *List[T]) Total() int { return len(l.items) }

// Items exposes the full item slice (before filtering), so callers can
// inspect list composition; tests of grouping rely on it.
func (l *List[T]) Items() []T { return l.items }

// Any reports whether any visible item matches pred, without moving the cursor.
func (l *List[T]) Any(pred func(T) bool) bool {
	for _, idx := range l.filtered {
		if pred(l.items[idx]) {
			return true
		}
	}
	return false
}

// Select moves the cursor to the first visible item matching pred, returning
// whether one was found.
func (l *List[T]) Select(pred func(T) bool) bool {
	for i, idx := range l.filtered {
		if pred(l.items[idx]) {
			l.cursor = i
			l.clampCursor()
			return true
		}
	}
	return false
}

// Selected returns the currently-highlighted item, or the zero value if empty
// (or if the cursor could only land on a non-selectable row).
func (l *List[T]) Selected() T {
	var zero T
	if l.cursor < 0 || l.cursor >= len(l.filtered) {
		return zero
	}
	it := l.items[l.filtered[l.cursor]]
	if !selectable(it) {
		return zero
	}
	return it
}

// Update handles navigation and filter-editing keys. It reports whether the
// key was consumed, so the host view can ignore keys the list handled.
func (l *List[T]) Update(msg tea.Msg) (consumed bool, cmd tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}

	if l.filtering {
		switch km.String() {
		case "esc":
			l.filtering = false
			l.query = ""
			l.applyFilter()
			return true, nil
		case "enter":
			l.filtering = false // keep the query, just stop editing
			return true, nil
		case "backspace":
			if l.query != "" {
				l.query = l.query[:len(l.query)-1]
				l.applyFilter()
			}
			return true, nil
		// Keys that can't be confused with typed text navigate while filtering,
		// so you can refine the query and move the selection at the same time.
		case "up":
			l.move(-1)
		case "down":
			l.move(1)
		case "ctrl+u":
			l.move(-l.visibleItems() / 2)
		case "ctrl+d":
			l.move(l.visibleItems() / 2)
		case "home":
			l.cursor = 0
		case "end":
			l.cursor = len(l.filtered) - 1
		default:
			// Append the key's text — a letter, digit, space, etc. Non-text keys
			// (arrows, …) have empty Text; control chars like Tab are ignored.
			if kp, ok := msg.(tea.KeyPressMsg); ok && kp.Text != "" {
				if r := []rune(kp.Text)[0]; r >= 0x20 && r != 0x7f {
					l.query += kp.Text
					l.applyFilter()
				}
			}
			return true, nil
		}
		l.clampCursor()
		return true, nil
	}

	switch {
	case key.Matches(km, l.keys.Up):
		l.move(-1)
	case key.Matches(km, l.keys.Down):
		l.move(1)
	case key.Matches(km, l.keys.HalfUp):
		l.move(-l.visibleItems() / 2)
	case key.Matches(km, l.keys.HalfDown):
		l.move(l.visibleItems() / 2)
	case key.Matches(km, l.keys.Top):
		l.cursor = 0
	case key.Matches(km, l.keys.Bottom):
		l.cursor = len(l.filtered) - 1
	case key.Matches(km, l.keys.Filter):
		l.filtering = true
	case key.Matches(km, l.keys.Clear):
		if l.query != "" {
			l.query = ""
			l.applyFilter()
		}
	default:
		return false, nil
	}
	l.clampCursor()
	return true, nil
}

func (l *List[T]) move(delta int) {
	if delta == 0 {
		return
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	l.cursor = clamp(l.cursor+delta, 0, max(0, len(l.filtered)-1))
	l.snap(dir)
}

// snap slides the cursor off non-selectable rows: first onward in dir, then
// back the other way if the list ends in separators.
func (l *List[T]) snap(dir int) {
	if len(l.filtered) == 0 {
		return
	}
	for i := l.cursor; i >= 0 && i < len(l.filtered); i += dir {
		if selectable(l.items[l.filtered[i]]) {
			l.cursor = i
			return
		}
	}
	for i := l.cursor; i >= 0 && i < len(l.filtered); i -= dir {
		if selectable(l.items[l.filtered[i]]) {
			l.cursor = i
			return
		}
	}
}

// ScrollBy moves the cursor by n rows (n<0 = up), clamped to the list bounds.
// Used for mouse-wheel scrolling; mirrors what the up/down keys do.
func (l *List[T]) ScrollBy(n int) {
	l.move(n)
	l.clampCursor()
}

// ClickAt selects the item drawn at line y of the list (0 = the first line
// View renders). Reports whether a selectable item was hit; separators,
// group headers and the blank space below the last item are not.
func (l *List[T]) ClickAt(y int) bool {
	if y < 0 {
		return false
	}
	row := y / max(l.rowHeight, 1)
	i := l.offset + row
	if row >= l.visibleItems() || i >= len(l.filtered) || !selectable(l.items[l.filtered[i]]) {
		return false
	}
	l.cursor = i
	l.clampCursor()
	return true
}

func (l *List[T]) clampCursor() {
	if len(l.filtered) == 0 {
		l.cursor, l.offset = 0, 0
		return
	}
	l.cursor = clamp(l.cursor, 0, len(l.filtered)-1)
	if !selectable(l.items[l.filtered[l.cursor]]) {
		l.snap(1)
	}
	// Keep the cursor within the visible window (measured in items).
	win := l.visibleItems()
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+win {
		l.offset = l.cursor - win + 1
	}
	// Pull group headers directly above the window into view while there's
	// slack: the cursor can never rest on a header, so a jump to the item
	// right under one would otherwise clip the header off-screen and make
	// the lane look like it vanished.
	for l.offset > 0 &&
		!selectable(l.items[l.filtered[l.offset-1]]) &&
		l.cursor-l.offset+1 < win {
		l.offset--
	}
	l.offset = clamp(l.offset, 0, max(0, len(l.filtered)-1))
}

func (l *List[T]) applyFilter() {
	l.filtered = l.filtered[:0]
	q := strings.TrimSpace(l.query)
	if !l.caseSensitive {
		q = strings.ToLower(q)
	}
	for i := range l.items {
		if q == "" {
			l.filtered = append(l.filtered, i)
			continue
		}
		// Separators drop out of a filtered list; matches render flat.
		if selectable(l.items[i]) && l.itemMatches(l.items[i], q) {
			l.filtered = append(l.filtered, i)
		}
	}
	l.clampCursor()
}

// itemMatches reports whether q (already case-folded if needed) matches any
// enabled field. Short metadata fields use fuzzy subsequence matching (so "snt"
// finds "sonnet"); prose fields (Prose() == true, e.g. a conversation body) use
// literal substring matching — subsequence matching on a large body matches
// almost any query and is never what the user means.
func (l *List[T]) itemMatches(it T, q string) bool {
	for _, f := range it.Fields() {
		if !l.fieldEnabled(f.Name) {
			continue
		}
		text := f.Text
		if !l.caseSensitive {
			text = strings.ToLower(text)
		}
		if f.Prose {
			if strings.Contains(text, q) {
				return true
			}
			continue
		}
		if matchesSubsequence(text, q) {
			return true
		}
	}
	return false
}

func (l *List[T]) View() string {
	if len(l.filtered) == 0 {
		empty := "No matches."
		if len(l.items) == 0 {
			empty = "Nothing here."
		}
		return lipgloss.NewStyle().Faint(true).Render(empty)
	}

	win := l.visibleItems()
	end := min(l.offset+win, len(l.filtered))

	// Reserve a right-hand gutter for the scrollbar (2 cols: bar + a gap) when
	// there's room, so item width stays stable whether or not it overflows.
	contentW := l.width
	gutter := l.width >= 3 && l.height > 0
	if gutter {
		contentW = l.width - 2
	}

	var lines []string
	for i := l.offset; i < end; i++ {
		block := l.items[l.filtered[i]].Render(contentW, i == l.cursor, l.highlighter())
		lines = append(lines, strings.Split(block, "\n")...)
	}
	if !gutter {
		return strings.Join(lines, "\n")
	}

	// Pad/clip to exactly the pane height, then attach the scrollbar column.
	for len(lines) < l.height {
		lines = append(lines, "")
	}
	lines = lines[:l.height]
	bar := Scrollbar(l.height, len(l.filtered), win, l.offset)
	for i := range lines {
		pad := max(0, contentW-lipgloss.Width(lines[i]))
		lines[i] += strings.Repeat(" ", pad) + " " + bar[i]
	}
	return strings.Join(lines, "\n")
}

// Scrollbar returns height cells for a slim vertical scrollbar: a thumb sized
// to the visible fraction and positioned by offset (a heavy line), over a faint
// light-line track. When everything fits (no overflow) it returns blanks, so a
// reserved gutter stays empty. Shared by the list rows and the preview pane.
func Scrollbar(height, total, visible, offset int) []string {
	track := Faint.Render("│")
	thumb := Text.Render("┃")

	out := make([]string, height)
	if total <= visible { // everything visible: no bar
		for i := range out {
			out[i] = " "
		}
		return out
	}
	size := min(max(1, height*visible/total), height)
	pos := (height - size) * offset / (total - visible)
	pos = clamp(pos, 0, height-size)
	for i := range out {
		if i >= pos && i < pos+size {
			out[i] = thumb
		} else {
			out[i] = track
		}
	}
	return out
}

// FilterLine renders the active filter prompt for a view header: the scoped
// fields (when not all-on), the query, a case marker, and matched/total count.
// Returns "" when there's no filter and we're not editing one.
func (l *List[T]) FilterLine() string {
	if !l.filtering && l.query == "" {
		return ""
	}
	faintStyle := lipgloss.NewStyle().Faint(true)

	var prefix string
	if scoped := l.EnabledFields(); len(scoped) > 0 {
		prefix = strings.Join(scoped, "+") + ": "
	} else {
		prefix = "/" // all-fields quick filter
	}

	cursor := ""
	if l.filtering {
		cursor = "█"
	}

	marker := ""
	if l.caseSensitive {
		marker = " Aa"
	}

	count := ""
	if l.query != "" {
		count = fmt.Sprintf("  %d/%d", l.Len(), l.Total())
	}

	return faintStyle.Render(prefix+l.query) + cursor + faintStyle.Render(marker+count)
}

// RevMarker is the suffix a view appends to its "sort: <mode>" status text when
// the sort order is reversed. Empty when it isn't, so callers can concatenate
// unconditionally.
func RevMarker(reversed bool) string {
	if reversed {
		return " (rev)"
	}
	return ""
}

// --- helpers ---------------------------------------------------------------

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// matchesSubsequence reports whether all runes of q appear in s in order.
func matchesSubsequence(s, q string) bool {
	if q == "" {
		return true
	}
	qi := 0
	qr := []rune(q)
	for _, sr := range s {
		if sr == qr[qi] {
			qi++
			if qi == len(qr) {
				return true
			}
		}
	}
	return false
}
