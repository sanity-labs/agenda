package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ScopeKind marks a search result that narrows the search instead of
// jumping: picking it makes its ID the scope of the next queries.
const ScopeKind = "scope"

// Searcher is a view that answers the quick-search box. Search returns a
// command that resolves to a SearchResultsMsg for exactly this query, at
// most limit results, in the view's current sort order.
type Searcher interface {
	Search(query, scope string, limit int) tea.Cmd
}

// SearchResultsMsg answers one Search call. The box drops it when the query
// or scope has moved on since.
type SearchResultsMsg struct {
	Query, Scope string
	Results      []Ref
	Err          error
}

// SearchAction is what the host should do after the box handled a key.
type SearchAction int

const (
	SearchNone    SearchAction = iota
	SearchChanged              // query or scope changed: search again
	SearchConfirm              // follow Selected
	SearchCancel
)

// SearchBox is the cmd+k modal: a query line, an optional scope chip, and
// the results of the last search that matched them.
type SearchBox struct {
	title   string
	Query   string
	Scope   string
	results []Ref
	labels  []string
	cursor  int
	rows    []int // box line each result starts on, as View last drew it
	Loading bool
	err     error
}

func NewSearchBox(title string) SearchBox { return SearchBox{title: title} }

// SetResults takes a view's answer if it is for the current query, with
// labels already rendered by the host (the ↗ marker depends on the views).
func (s *SearchBox) SetResults(msg SearchResultsMsg, labels []string) bool {
	if msg.Query != s.Query || msg.Scope != s.Scope {
		return false
	}
	s.results, s.labels, s.err, s.Loading, s.cursor = msg.Results, labels, msg.Err, false, 0
	return true
}

// Selected is the highlighted result, or false when there is none.
func (s *SearchBox) Selected() (Ref, bool) {
	if s.cursor >= len(s.results) {
		return Ref{}, false
	}
	return s.results[s.cursor], true
}

// Narrow makes the scope result's ID the new scope and clears the query.
func (s *SearchBox) Narrow(scope string) {
	s.Scope, s.Query, s.results, s.labels, s.cursor = scope, "", nil, nil, 0
}

func (s *SearchBox) Update(msg tea.Msg) SearchAction {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return SearchNone
	}
	switch km.String() {
	case "esc", "ctrl+c":
		return SearchCancel
	case "enter":
		if _, ok := s.Selected(); ok {
			return SearchConfirm
		}
		return SearchNone
	case "up", "ctrl+p":
		s.cursor = max(s.cursor-1, 0)
		return SearchNone
	case "down", "ctrl+n":
		s.cursor = min(s.cursor+1, max(len(s.results)-1, 0))
		return SearchNone
	case "backspace":
		switch {
		case s.Query != "":
			r := []rune(s.Query)
			s.Query = string(r[:len(r)-1])
		case s.Scope != "":
			// An empty query has nothing left to delete but the chip.
			s.Scope = ""
		default:
			return SearchNone
		}
		return SearchChanged
	}
	if km.Text != "" {
		if r := []rune(km.Text)[0]; r >= 0x20 && r != 0x7f {
			s.Query += km.Text
			return SearchChanged
		}
	}
	return SearchNone
}

const searchWidth = 72

// searchTop is the border plus top padding View puts above the title.
const searchTop = 2

// SelectAt moves the cursor to the result drawn on box line y, its detail
// line included, and reports whether there was one.
func (s *SearchBox) SelectAt(y int) bool {
	for i := len(s.rows) - 1; i >= 0; i-- {
		if y >= s.rows[i] && y < s.rows[i]+2 && (y == s.rows[i] || s.results[i].Detail != "") {
			s.cursor = i
			return true
		}
	}
	return false
}

func (s *SearchBox) View() string {
	bold := lipgloss.NewStyle().Bold(true)
	var b strings.Builder
	b.WriteString(bold.Render(s.title))
	b.WriteString("\n\n")
	if s.Scope != "" {
		b.WriteString(Accent.Render(s.Scope) + Faint.Render(" › "))
	}
	b.WriteString(s.Query + Accent.Render("▏"))
	b.WriteString("\n\n")
	switch {
	case s.Query == "":
		b.WriteString(Faint.Render("Type a command or search"))
		b.WriteByte('\n')
	case s.err != nil:
		b.WriteString(Red.Render(Truncate(s.err.Error(), searchWidth)))
		b.WriteByte('\n')
	case s.Loading && len(s.results) == 0:
		b.WriteString(Faint.Render("searching…"))
		b.WriteByte('\n')
	case len(s.results) == 0:
		b.WriteString(Faint.Render("no matches"))
		b.WriteByte('\n')
	}
	s.rows = s.rows[:0]
	for i, label := range s.labels {
		s.rows = append(s.rows, searchTop+strings.Count(b.String(), "\n"))
		line := Truncate(label, searchWidth-2)
		if i == s.cursor {
			b.WriteString(Accent.Render("▌ ") + bold.Render(line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteByte('\n')
		if d := s.results[i].Detail; d != "" {
			b.WriteString("    " + Faint.Render(Truncate(d, searchWidth-4)) + "\n")
		}
	}
	b.WriteByte('\n')
	hint := "↑/↓ select · enter open · esc close"
	if s.Scope != "" {
		hint += " · ⌫ drops the repo"
	}
	b.WriteString(Faint.Render(hint))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Pal().Accent)).
		Padding(1, 2).
		Width(searchWidth + 6).
		Render(b.String())
}
