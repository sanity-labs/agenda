package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/herdr"
	"github.com/sanity-labs/agenda/internal/ui"
)

type repoView struct {
	fetchView
	shown string
	set   bool
}

func (r *repoView) ShowRepo(repo string) tea.Cmd { r.shown, r.set = repo, true; return nil }
func (r *repoView) LeaveRepo() bool              { was := r.set; r.set = false; return was }

// An Open ref is followed and zoomed once the screen has a size, on the
// view that owns the ref's kind.
func TestOpenRefJumpsAndZooms(t *testing.T) {
	fv := &fetchView{fatView: fatView{title: "Linear"}}
	m := New(config.Default(), []View{&fatView{title: "PRs"}, fv}).
		WithOpen(Open{Ref: ui.Ref{Kind: "linear", ID: "SRE-1"}})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(Model)
	if m.current != 1 || fv.fetched != "SRE-1" || !m.zoomed {
		t.Errorf("current=%d fetched=%q zoomed=%v", m.current, fv.fetched, m.zoomed)
	}
}

// A repo open lists it on the focused view, and esc on the bare list
// leaves it.
func TestOpenRepoShowsAndEscLeaves(t *testing.T) {
	rv := &repoView{fetchView: fetchView{fatView: fatView{title: "PRs"}}}
	m := New(config.Default(), []View{rv}).WithOpen(Open{Repo: "o/r"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(Model)
	if rv.shown != "o/r" || m.zoomed {
		t.Fatalf("shown=%q zoomed=%v", rv.shown, m.zoomed)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if rv.set {
		t.Error("esc did not leave the repo list")
	}
}

func TestHerdrEscLeavesRepoBeforeClosing(t *testing.T) {
	rv := &repoView{fetchView: fetchView{fatView: fatView{title: "PRs"}}}
	m := New(config.Default(), []View{rv}).WithHerdr(&herdr.Opener{}).WithOpen(Open{Repo: "o/r"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(Model)
	for i, want := range []bool{false, true} {
		got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		m = got.(Model)
		quit := false
		if cmd != nil {
			_, quit = cmd().(tea.QuitMsg)
		}
		if quit != want || (i == 0 && rv.set) {
			t.Fatalf("esc #%d: quit=%v want %v, repo still set=%v", i+1, quit, want, rv.set)
		}
	}
}
