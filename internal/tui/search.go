package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// searchTickMsg fires once typing has paused; gen says whether a later key
// superseded it.
type searchTickMsg struct{ gen int }

const searchDebounce = 250 * time.Millisecond

// openSearch raises the quick-search box over a view that can answer it.
func (m *Model) openSearch() bool {
	if _, ok := m.views[m.current].(ui.Searcher); !ok {
		return false
	}
	b := ui.NewSearchBox("Search " + m.views[m.current].Title())
	m.search, m.searchView = &b, m.current
	return true
}

func (m *Model) updateSearch(msg tea.KeyMsg) tea.Cmd {
	switch m.search.Update(msg) {
	case ui.SearchCancel:
		m.search = nil
	case ui.SearchChanged:
		return m.searchSoon()
	case ui.SearchConfirm:
		ref, _ := m.search.Selected()
		if ref.Kind == ui.ScopeKind {
			m.search.Narrow(ref.ID)
			return nil
		}
		m.search = nil
		return m.followRef(ref)
	}
	return nil
}

func (m *Model) searchSoon() tea.Cmd {
	m.searchGen++
	gen := m.searchGen
	return tea.Tick(searchDebounce, func(time.Time) tea.Msg { return searchTickMsg{gen} })
}

func (m *Model) runSearch(gen int) tea.Cmd {
	if m.search == nil || gen != m.searchGen {
		return nil
	}
	if m.search.Query == "" {
		m.search.SetResults(ui.SearchResultsMsg{Scope: m.search.Scope}, nil)
		return nil
	}
	m.search.Loading = true
	return m.views[m.searchView].(ui.Searcher).Search(m.search.Query, m.search.Scope)
}

func (m *Model) takeSearchResults(msg ui.SearchResultsMsg) {
	if m.search == nil {
		return
	}
	labels := make([]string, len(msg.Results))
	for i, r := range msg.Results {
		labels[i] = r.Label
		if r.Kind != ui.ScopeKind && !m.resolves(r) {
			labels[i] += "  ↗"
		}
	}
	m.search.SetResults(msg, labels)
}
