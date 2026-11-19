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

func TestPaletteFreeTextLevelSubmitsTheLine(t *testing.T) {
	var got []string
	lv := &textView{levelView: levelView{searchView: searchView{jumpView: jumpView{fatView: fatView{title: "Linear"}, id: "u1"}}}, got: &got}
	m := New(config.Default(), []View{lv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m = typeText(m, "comment")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // blank: nothing to submit
	if m.search == nil || len(got) != 0 {
		t.Fatalf("a blank line should not submit; open=%v got=%v", m.search != nil, got)
	}
	m = typeText(m, "hi there")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.search != nil || len(got) != 1 || got[0] != "hi there" {
		t.Errorf("open=%v got=%v, want the line submitted once and the box closed", m.search != nil, got)
	}
}

type textView struct {
	levelView
	got *[]string
}

func (l *textView) Commands() []ui.Command {
	return []ui.Command{{ID: "comment", Title: "Comment", Next: func() *ui.Menu {
		return &ui.Menu{Title: "Comment", Text: func(s string) tea.Cmd { *l.got = append(*l.got, s); return nil }}
	}}}
}

func TestPaletteMenuFallbackOffersTheTypedQuery(t *testing.T) {
	var made []string
	lv := &fallbackView{levelView: levelView{searchView: searchView{jumpView: jumpView{fatView: fatView{title: "Linear"}, id: "u1"}}}, made: &made}
	m := New(config.Default(), []View{lv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m = typeText(m, "labels")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeText(m, "bug")
	if v := m.search.View(); strings.Contains(v, "Create") {
		t.Fatalf("a matching option should hide the fallback:\n%s", v)
	}
	m = typeText(m, "gy")
	if v := m.search.View(); !strings.Contains(v, `Create "buggy"`) {
		t.Fatalf("no match should offer the typed query:\n%s", v)
	}
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.search == nil || m.search.Path != "Labels › Create" {
		t.Fatalf("the fallback should open its own level, path=%v", m.search)
	}
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(made) != 1 || made[0] != "buggy" || m.search != nil {
		t.Errorf("made=%v open=%v, want buggy created and the box closed", made, m.search != nil)
	}
}

type fallbackView struct {
	levelView
	made *[]string
}

func (l *fallbackView) Commands() []ui.Command {
	return []ui.Command{{ID: "labels", Title: "Labels", Next: func() *ui.Menu {
		return &ui.Menu{
			Title:   "Labels",
			Options: []ui.Command{{ID: "bug", Title: "bug", Run: func() tea.Cmd { return nil }}},
			Fallback: func(q string) []ui.Command {
				return []ui.Command{{ID: "create", Title: `Create "` + q + `"`, Next: func() *ui.Menu {
					return &ui.Menu{Title: "Create", Options: []ui.Command{{ID: "go", Title: "go", Run: func() tea.Cmd { *l.made = append(*l.made, q); return nil }}}}
				}}}
			},
		}
	}}}
}

type matchView struct{ levelView }

func (l *matchView) Commands() []ui.Command {
	return []ui.Command{{ID: "who", Title: "Who", Next: func() *ui.Menu {
		return &ui.Menu{
			Title: "Who",
			Match: func(q string) []ui.Command {
				if q == "" {
					return []ui.Command{{ID: "a", Title: "first"}, {ID: "b", Title: "second"}}
				}
				if q == "zz" { // a hit the substring filter would drop
					return []ui.Command{{ID: "z", Title: "unrelated title"}}
				}
				return nil
			},
			Fallback: func(q string) []ui.Command { return []ui.Command{{ID: "f", Title: `Request "` + q + `"`}} },
		}
	}}}
}

func TestPaletteMenuMatchOwnsTheRows(t *testing.T) {
	lv := &matchView{levelView{searchView: searchView{jumpView: jumpView{fatView: fatView{title: "PRs"}, id: "u1"}}}}
	m := New(config.Default(), []View{lv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m = typeText(m, "who")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if v := m.search.View(); !strings.Contains(v, "first") || !strings.Contains(v, "second") {
		t.Fatalf("empty query should list Match(\"\"):\n%s", v)
	}
	m = typeText(m, "zz")
	if v := m.search.View(); !strings.Contains(v, "unrelated title") {
		t.Fatalf("Match rows are not re-filtered by substring:\n%s", v)
	}
	m = typeText(m, "x")
	if v := m.search.View(); !strings.Contains(v, `Request "zzx"`) {
		t.Fatalf("no Match rows should fall back:\n%s", v)
	}
}

type pendingView struct {
	levelView
	pending bool
}

func (l *pendingView) Commands() []ui.Command {
	return []ui.Command{{ID: "who", Title: "Who", Next: func() *ui.Menu {
		return &ui.Menu{
			Title:   "Who",
			Match:   func(string) []ui.Command { return nil },
			Pending: func() bool { return l.pending },
		}
	}}}
}

func TestPaletteMenuPendingShowsSearching(t *testing.T) {
	lv := &pendingView{levelView: levelView{searchView: searchView{jumpView: jumpView{fatView: fatView{title: "PRs"}, id: "u1"}}}, pending: true}
	m := New(config.Default(), []View{lv})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m, _ = hit(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m = typeText(m, "who")
	m, _ = hit(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if v := m.search.View(); !strings.Contains(v, "searching…") {
		t.Fatalf("a pending menu should say so:\n%s", v)
	}
	lv.pending = false
	got, _ := m.Update(ui.CommandsChangedMsg{})
	m = got.(Model)
	if v := m.search.View(); strings.Contains(v, "searching…") {
		t.Fatalf("still searching once loaded:\n%s", v)
	}
}
