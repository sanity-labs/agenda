package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// levelView has one command that opens a static list whose options open a
// picker, the shape of Link related issue.
type levelView struct {
	searchView
	picked string
	asked  string
}

func (l *levelView) PrepareCommands() tea.Cmd { return nil }
func (l *levelView) Commands() []ui.Command {
	return []ui.Command{
		{ID: "merge", Title: "Merge", Run: func() tea.Cmd { return nil }},
		{ID: "link", Title: "Link", Aliases: []string{"mark"}, Next: func() *ui.Menu {
			return &ui.Menu{Title: "Link", Options: []ui.Command{
				{ID: "blocks", Title: "blocks", Next: func() *ui.Menu {
					return &ui.Menu{Title: "blocks", Hint: "Type an issue",
						Search: func(q string, _ int) tea.Cmd {
							l.asked = q
							return func() tea.Msg {
								return ui.SearchResultsMsg{Query: q, Results: []ui.Ref{{Kind: "linear", ID: "SRE-9", Label: "SRE-9", Detail: "nine"}}}
							}
						},
						Pick: func(r ui.Ref) tea.Cmd { l.picked = r.ID; return nil }}
				}},
				{ID: "related", Title: "related to", Run: func() tea.Cmd { l.picked = "related"; return nil }},
			}}
		}},
	}
}

func levelModel(t *testing.T) (Model, *levelView) {
	t.Helper()
	lv := &levelView{searchView: searchView{jumpView: jumpView{fatView: fatView{title: "Linear"}, id: "u1"}}}
	m := New(config.Default(), []View{lv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	return m, lv
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = hit(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func TestPaletteOpensLevelsAndEscGoesBackOne(t *testing.T) {
	m, lv := levelModel(t)
	m = typeText(m, "mark") // matched through the alias
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.search == nil || !strings.Contains(m.search.View(), "related to") || m.search.Path != "Link" {
		t.Fatalf("level two should list the options under Link:\n%s", m.search.View())
	}
	t.Log("\n" + m.search.View())
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // blocks -> picker
	if m.search.Path != "Link › blocks" || !strings.Contains(m.search.View(), "Type an issue") {
		t.Fatalf("level three should be the picker:\n%s", m.search.View())
	}
	t.Log("\n" + m.search.View())
	m = typeText(m, "SRE")
	cmd := m.runSearch(m.searchGen)
	if cmd == nil || lv.asked != "SRE" {
		t.Fatalf("typing at the picker should ask the menu's search, asked=%q", lv.asked)
	}
	got, _ := m.Update(cmd())
	m = got.(Model)
	if v := m.search.View(); !strings.Contains(v, "SRE-9") || strings.Contains(v, "↗") {
		t.Fatalf("picker results:\n%s", v)
	}
	t.Log("\n" + m.search.View())

	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search == nil || m.search.Path != "Link" || m.search.Query != "" {
		t.Fatalf("esc should go back to level two, path=%q", m.search.Path)
	}
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search == nil || m.search.Path != "" || !strings.Contains(m.search.View(), "Merge") {
		t.Fatal("esc should go back to the top level")
	}
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search != nil {
		t.Error("esc at the top should close the palette")
	}
}

func TestPaletteLastLevelRunsAndCloses(t *testing.T) {
	m, lv := levelModel(t)
	m = typeText(m, "link")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if lv.picked != "related" || m.search != nil {
		t.Errorf("picked=%q open=%v, want the option run and the box closed", lv.picked, m.search != nil)
	}

	m, lv = levelModel(t)
	m = typeText(m, "link")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeText(m, "9")
	got, _ := m.Update(ui.SearchResultsMsg{Query: "9", Results: []ui.Ref{{Kind: "linear", ID: "SRE-9", Label: "SRE-9"}}})
	m = got.(Model)
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if lv.picked != "SRE-9" || m.search != nil {
		t.Errorf("picked=%q open=%v, want SRE-9 handed to Pick", lv.picked, m.search != nil)
	}
}
