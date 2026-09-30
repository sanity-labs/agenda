package prs

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// rowSettle fires once the selection has stopped moving, to refresh the
// row under the cursor. Generation-stamped like settleMsg: cycling through
// a list supersedes every earlier tick, so twenty rows cost one request.
type rowSettleMsg struct {
	gen int
	url string
}

// rowFreshMsg carries a re-read of one PR.
type rowFreshMsg struct {
	url string
	pr  pr
	ok  bool
}

// rowSettleDelay is long enough that j/k through a list refreshes nothing
// until you stop, short enough that stopping feels like it acted.
const rowSettleDelay = 400 * time.Millisecond

// scheduleRowRefresh debounces a re-read of the selected PR. Check state
// and review decisions change on GitHub's clock, not the refresh timer's,
// so the row you are about to act on is the one worth keeping current.
func (v *View) scheduleRowRefresh() tea.Cmd {
	if !v.rowRefresh {
		return nil
	}
	url := v.list.Selected().URL
	if url == "" {
		return nil
	}
	v.rowGen++
	gen := v.rowGen
	return tea.Tick(rowSettleDelay, func(time.Time) tea.Msg {
		return rowSettleMsg{gen: gen, url: url}
	})
}

// refreshRow re-reads one PR. It goes through the same search the list
// uses, scoped to a single PR, so the fields and decoding stay identical
// rather than drifting from a second query.
func (v *View) refreshRow(url string) tea.Cmd {
	p, ok := v.prByURL(url)
	if !ok || p.repo() == "" || p.Number == 0 {
		return nil
	}
	q := fmt.Sprintf("repo:%s is:pr %d", p.repo(), p.Number)
	return func() tea.Msg {
		page, _, _ := searchPRs(q, 1, "")
		for _, got := range page.prs {
			if got.URL == url {
				return rowFreshMsg{url: url, pr: got, ok: true}
			}
		}
		return rowFreshMsg{url: url}
	}
}

// applyFresh replaces a row in place, keeping the local-only fields the
// fetch cannot know about (the unread mark is set by the list's own diff,
// and clobbering it would re-mark a row you have read).
func (v *View) applyFresh(fresh pr) bool {
	for _, set := range [][]pr{v.raw, v.reviewRaw} {
		for i := range set {
			if set[i].URL != fresh.URL {
				continue
			}
			keepUnread, keepGutter := set[i].Unread, set[i].UnreadGutter
			set[i] = fresh
			set[i].Unread, set[i].UnreadGutter = keepUnread, keepGutter
			return true
		}
	}
	return false
}
