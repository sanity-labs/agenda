package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// cmdView offers two commands on its selected item and answers searches
// with nothing.
type cmdView struct {
	searchView
	ran string
}

func (c *cmdView) PrepareCommands() tea.Cmd { return nil }
func (c *cmdView) Commands() []ui.Command {
	mk := func(id, title string) ui.Command {
		return ui.Command{ID: id, Title: title, Run: func() tea.Cmd { c.ran = id; return nil }}
	}
	return []ui.Command{mk("merge", "Merge"), mk("close", "Close with comment")}
}

func paletteModel(t *testing.T) (Model, *cmdView) {
	t.Helper()
	cv := &cmdView{searchView: searchView{jumpView: jumpView{fatView: fatView{title: "PRs"}, id: "u1"}}}
	m := New(config.Default(), []View{cv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	return m, cv
}

func TestPaletteListsAndFiltersCommands(t *testing.T) {
	m, _ := paletteModel(t)
	if v := m.search.View(); !strings.Contains(v, "Merge") || !strings.Contains(v, "Close with comment") {
		t.Fatalf("empty box should list the commands:\n%s", v)
	}
	for _, r := range "mer" {
		m, _ = hit(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	v := m.search.View()
	if !strings.Contains(v, "Merge") || strings.Contains(v, "Close with comment") {
		t.Errorf(`"mer" should keep only Merge:\n%s`, v)
	}
}

func TestPaletteEnterRunsCommandOnTheView(t *testing.T) {
	m, cv := paletteModel(t)
	for _, r := range "clo" {
		m, _ = hit(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cv.ran != "close" || m.search != nil {
		t.Errorf("ran=%q, box open=%v; want close run and box closed", cv.ran, m.search != nil)
	}
}
