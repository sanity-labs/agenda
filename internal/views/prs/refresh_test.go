package prs

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

func refreshView(t *testing.T, prs ...pr) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil) // refresh_row defaults on
	v.SetSize(80, 60, 40)
	v.Update(mineMsg{page: searchPage{prs: prs}})
	return v
}

func row(num int, url string) pr {
	p := pr{Number: num, URL: url, Title: "t", Mergeable: "MERGEABLE"}
	p.Repository.NameWithOwner = "o/r"
	return p
}

// The debounce is the whole point: cycling a list must not fire a request
// per keypress, so every tick but the last is superseded.
func TestCyclingSupersedesEarlierRefreshTicks(t *testing.T) {
	// Enough rows that every 'j' actually moves: a move that stays put
	// stamps no new generation, so the earlier tick is still the current one.
	v := refreshView(t, row(1, "u1"), row(2, "u2"), row(3, "u3"), row(4, "u4"))

	// Each move stamps a generation; only the newest may survive.
	var ticks []rowSettleMsg
	for i := 0; i < 3; i++ {
		v.Update(tea.KeyPressMsg{Code: 'j'})
		ticks = append(ticks, rowSettleMsg{gen: v.rowGen, url: v.list.Selected().URL})
	}
	if ticks[0].gen == ticks[2].gen {
		t.Fatalf("the moves stamped one generation (%d), so nothing is debounced", ticks[0].gen)
	}
	for _, stale := range ticks[:len(ticks)-1] {
		if cmd := v.Update(stale); cmd != nil {
			t.Errorf("a superseded tick (gen %d) still fetched", stale.gen)
		}
	}
	if cmd := v.Update(ticks[len(ticks)-1]); cmd == nil {
		t.Error("the newest tick did not fetch")
	}

	// The generation check has to carry its own weight: a stale tick naming
	// the row that happens to be selected again (cycle away and back, or a
	// re-sort) is still stale, and only the generation says so.
	current := v.list.Selected().URL
	if cmd := v.Update(rowSettleMsg{gen: ticks[0].gen, url: current}); cmd != nil {
		t.Error("an old tick fetched because its row was selected again")
	}
}

// A tick whose row is no longer selected is dropped: refreshing the row
// you already left is the bug the generation check alone would not catch,
// since a re-entered row can share the newest generation.
func TestRefreshSkipsARowYouLeft(t *testing.T) {
	v := refreshView(t, row(1, "u1"), row(2, "u2"))
	v.Update(tea.KeyPressMsg{Code: 'j'})
	stale := rowSettleMsg{gen: v.rowGen, url: "u1"} // current gen, wrong row
	if cmd := v.Update(stale); cmd != nil {
		t.Error("refreshed a row that is no longer selected")
	}
}

// Turning it off means no ticks at all.
func TestRefreshOffSchedulesNothing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	off := false
	v := New(config.GitHubConfig{RefreshRow: &off}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	v.Update(mineMsg{page: searchPage{prs: []pr{row(1, "u1"), row(2, "u2")}}})
	if cmd := v.scheduleRowRefresh(); cmd != nil {
		t.Error("scheduled a refresh with github.refresh_row off")
	}
}

// A refresh replaces the row's server-owned fields but must not clobber
// the unread mark, which only the list's own diff knows about.
func TestFreshRowKeepsTheUnreadMark(t *testing.T) {
	v := refreshView(t, row(1, "u1"))
	v.raw[0].Unread, v.raw[0].UnreadGutter = true, true

	fresh := row(1, "u1")
	fresh.ReviewDecision = "APPROVED"
	if !v.applyFresh(fresh) {
		t.Fatal("applyFresh did not find the row")
	}
	if got := v.raw[0].ReviewDecision; got != "APPROVED" {
		t.Errorf("review decision = %q, want the fresh value", got)
	}
	if !v.raw[0].Unread || !v.raw[0].UnreadGutter {
		t.Error("the refresh cleared the unread mark")
	}
}

// A row that is not loaded (or has no repo) is not fetched: there is
// nothing to address the query to.
func TestRefreshIgnoresUnknownRows(t *testing.T) {
	v := refreshView(t, row(1, "u1"))
	if cmd := v.refreshRow("nope"); cmd != nil {
		t.Error("fetched a URL that is not in either section")
	}
}
