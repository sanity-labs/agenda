package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

type pausableView struct {
	fatView
	paused bool
	inits  int
}

func (p *pausableView) Init() tea.Cmd { p.inits++; return nil }
func (p *pausableView) Paused() bool  { return p.paused }

func TestRefreshTickSkipsAPausedView(t *testing.T) {
	v := &pausableView{fatView: fatView{title: "PRs"}, paused: true}
	cfg := config.Default()
	cfg.Refresh.Every = config.Duration(time.Minute)
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	v.inits = 0

	_, cmd := m.Update(refreshTickMsg{view: 0, gen: m.refreshGen})
	if v.inits != 0 {
		t.Fatalf("a paused view was refreshed %d times", v.inits)
	}
	if cmd == nil {
		t.Fatal("the tick was not rescheduled, so the view never resumes")
	}
	v.paused = false
	m.Update(refreshTickMsg{view: 0, gen: m.refreshGen})
	if v.inits != 1 {
		t.Fatalf("after the pause, inits = %d, want 1", v.inits)
	}
}
