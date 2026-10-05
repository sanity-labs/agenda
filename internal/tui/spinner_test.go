package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

// loadOnKeyView starts loading when it sees 'x', the way a view does when
// the end of its list asks for the next page.
type loadOnKeyView struct {
	fatView
	loading bool
}

func (v *loadOnKeyView) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.Code == 'x' {
		v.loading = true
	}
	return nil
}
func (v *loadOnKeyView) Loading() bool { return v.loading }

// The spinner loop only runs while something loads and used to be started
// by Init and ctrl+r alone, so a view that began loading on its own showed
// a frozen glyph. A key that starts a load now restarts the loop.
func TestSpinnerRestartsWhenAViewStartsLoadingOnItsOwn(t *testing.T) {
	v := &loadOnKeyView{fatView: fatView{title: "Linear"}}
	cfg := config.Default()
	cfg.Refresh.Every = 0
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd == nil {
		t.Fatal("a key that started a load returned no command")
	}
	done := make(chan bool, 1)
	go func() {
		var tick bool
		var walk func(tea.Cmd)
		walk = func(c tea.Cmd) {
			if c == nil {
				return
			}
			switch msg := c().(type) {
			case tea.BatchMsg:
				for _, sub := range msg {
					walk(sub)
				}
			case spinnerTickMsg:
				tick = true
			}
		}
		walk(cmd)
		done <- tick
	}()
	select {
	case tick := <-done:
		if !tick {
			t.Error("no spinner tick was scheduled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the command did not complete")
	}
}
