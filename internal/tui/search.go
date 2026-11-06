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
	b := ui.NewSearchBox(m.views[m.current].Title())
	m.search, m.searchView = &b, m.current
	m.showCommands()
	return true
}

// prepareCommands warms the commands of the view the box is over.
func (m *Model) prepareCommands() tea.Cmd {
	if c, ok := m.views[m.searchView].(ui.Commander); ok {
		return c.PrepareCommands()
	}
	return nil
}

// commandRefs are the view's commands matching the query, as results; none
// once the box is narrowed to a repo, which is a search.
func (m *Model) commandRefs() []ui.Ref {
	c, ok := m.views[m.searchView].(ui.Commander)
	if !ok || m.search.Scope != "" {
		return nil
	}
	var refs []ui.Ref
	for _, cmd := range ui.MatchCommands(c.Commands(), m.search.Query) {
		refs = append(refs, ui.Ref{Kind: ui.CommandKind, ID: cmd.ID, Label: cmd.Title, Detail: cmd.Detail})
	}
	return refs
}

// showCommands lists the matching commands at once, ahead of the debounced
// search whose results are added below them.
func (m *Model) showCommands() {
	refs := m.commandRefs()
	labels := make([]string, len(refs))
	for i, r := range refs {
		labels[i] = r.Label
	}
	m.search.SetResults(ui.SearchResultsMsg{Query: m.search.Query, Scope: m.search.Scope, Results: refs}, labels)
	m.search.Loading = m.search.Query != ""
}

func (m *Model) runCommand(id string) tea.Cmd {
	c, _ := m.views[m.searchView].(ui.Commander)
	if c == nil {
		return nil
	}
	for _, cmd := range c.Commands() {
		if cmd.ID == id {
			return cmd.Run()
		}
	}
	return nil
}

func (m *Model) updateSearch(msg tea.KeyMsg) tea.Cmd {
	switch m.search.Update(msg) {
	case ui.SearchCancel:
		m.search = nil
	case ui.SearchChanged:
		m.showCommands()
		return m.searchSoon()
	case ui.SearchConfirm:
		ref, _ := m.search.Selected()
		if ref.Kind == ui.ScopeKind {
			m.search.Narrow(ref.ID)
			return nil
		}
		m.search = nil
		if ref.Kind == ui.CommandKind {
			return m.runCommand(ref.ID)
		}
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
		m.showCommands()
		return nil
	}
	m.search.Loading = true
	return m.views[m.searchView].(ui.Searcher).Search(m.search.Query, m.search.Scope, m.cfg.SearchLimit())
}

func (m *Model) takeSearchResults(msg ui.SearchResultsMsg) {
	if m.search == nil {
		return
	}
	if msg.Query == m.search.Query && msg.Scope == m.search.Scope {
		msg.Results = append(m.commandRefs(), msg.Results...)
	}
	labels := make([]string, len(msg.Results))
	for i, r := range msg.Results {
		labels[i] = r.Label
		if r.Kind != ui.ScopeKind && r.Kind != ui.CommandKind && !m.resolves(r) {
			labels[i] += "  ↗"
		}
	}
	m.search.SetResults(msg, labels)
}
