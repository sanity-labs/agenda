package tui

import (
	"strings"
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
	m.search, m.searchView, m.menus = &b, m.current, nil
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
	cmds := m.levelCommands()
	if cmds == nil || m.search.Scope != "" {
		return nil
	}
	var refs []ui.Ref
	for _, cmd := range ui.MatchCommands(cmds, m.search.Query) {
		refs = append(refs, ui.Ref{Kind: ui.CommandKind, ID: cmd.ID, Label: cmd.Title, Detail: cmd.Detail})
	}
	return refs
}

// topMenu is the open sub-level, or nil at the top.
func (m *Model) topMenu() *ui.Menu {
	if len(m.menus) == 0 {
		return nil
	}
	return m.menus[len(m.menus)-1]
}

// levelCommands are the rows of the current level: the view's commands at
// the top, a menu's options below it, none for a picker.
func (m *Model) levelCommands() []ui.Command {
	if mn := m.topMenu(); mn != nil {
		return mn.Options
	}
	c, _ := m.views[m.searchView].(ui.Commander)
	if c == nil {
		return nil
	}
	return c.Commands()
}

// syncMenu points the box at the current level after a push or a pop.
func (m *Model) syncMenu() {
	path := make([]string, len(m.menus))
	for i, mn := range m.menus {
		path[i] = mn.Title
	}
	m.search.Path, m.search.Hint = strings.Join(path, " › "), ""
	if mn := m.topMenu(); mn != nil {
		m.search.Hint = mn.Hint
	}
	m.search.Query, m.search.Scope = "", ""
	m.searchGen++
	m.showCommands()
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
	mn := m.topMenu()
	m.search.Loading = m.search.Query != "" && (mn == nil || mn.Search != nil)
}

// runCommand runs the chosen command, or opens its next level and keeps
// the box up.
func (m *Model) runCommand(id string) tea.Cmd {
	for _, cmd := range m.levelCommands() {
		if cmd.ID != id {
			continue
		}
		if cmd.Next != nil {
			m.menus = append(m.menus, cmd.Next())
			m.syncMenu()
			return nil
		}
		m.search = nil
		return cmd.Run()
	}
	m.search = nil
	return nil
}

func (m *Model) updateSearch(msg tea.KeyMsg) tea.Cmd {
	switch m.search.Update(msg) {
	case ui.SearchCancel:
		if len(m.menus) > 0 {
			m.menus = m.menus[:len(m.menus)-1]
			m.syncMenu()
			return nil
		}
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
		if ref.Kind == ui.CommandKind {
			return m.runCommand(ref.ID)
		}
		if mn := m.topMenu(); mn != nil && mn.Pick != nil {
			m.search = nil
			return mn.Pick(ref)
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
		m.showCommands()
		return nil
	}
	if mn := m.topMenu(); mn != nil && mn.Search == nil {
		return nil
	}
	m.search.Loading = true
	if mn := m.topMenu(); mn != nil && mn.Search != nil {
		return mn.Search(m.search.Query, m.cfg.SearchLimit())
	}
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
		if r.Kind != ui.ScopeKind && r.Kind != ui.CommandKind && m.topMenu() == nil && !m.resolves(r) {
			labels[i] += "  ↗"
		}
	}
	m.search.SetResults(msg, labels)
}
