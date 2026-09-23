package prs

import (
	"errors"

	"github.com/sanity-labs/agenda/internal/config"
	"strings"
	"testing"
	"time"
)

func TestMineAndReviewDeliverIndependently(t *testing.T) {
	v := &View{showReview: true, loading: true}
	v.list.SetRowHeight(2)

	// Own PRs land first: list usable, review section still pending.
	v.Update(mineMsg{prs: []pr{mkPR("u1", "mine", time.Hour)}})
	if v.loading || v.err != nil {
		t.Fatalf("mine delivery: loading=%v err=%v", v.loading, v.err)
	}
	if v.list.Total() != 1 {
		t.Fatalf("list rows = %d, want the own PR only", v.list.Total())
	}

	// Review search fails: the tab stays alive, the section says so.
	v.reviewLoading = true
	v.Update(reviewListMsg{err: errors.New("boom")})
	if v.err != nil {
		t.Error("a review-search failure must not become a tab error")
	}
	if v.reviewLoading {
		t.Error("reviewLoading should clear on failure")
	}
	found := false
	for _, p := range v.list.Items() {
		if p.Separator != "" && !p.Group && strings.Contains(p.Separator, "fetch failed") {
			found = true
		}
	}
	if !found {
		t.Error("no section band reported the failed review search")
	}

	// A later success replaces the error and fills the section.
	v.Update(reviewListMsg{prs: []pr{mkPR("u2", "theirs", time.Hour)}})
	if v.reviewErr != nil {
		t.Error("reviewErr should clear on success")
	}
	if v.list.Total() != 4 {
		t.Errorf("rows = %d, want mine + 2 section bands + review PR", v.list.Total())
	}
}

func TestShowReviewDefaultsOff(t *testing.T) {
	v := New(config.GitHubConfig{}, nil, nil, nil)
	if v.showReview {
		t.Error("review section must default off, matching config.ShowReviewRequested")
	}
	on := true
	if v := New(config.GitHubConfig{ShowReviewRequested: &on}, nil, nil, nil); !v.showReview {
		t.Error("explicit true should enable the section")
	}
}

func TestSettleDebounce(t *testing.T) {
	v := &View{pane: paneDiff}
	v.list.SetRowHeight(2)
	// Two rapid navigations: only the second settle generation acts.
	first := v.scheduleSettle()
	second := v.scheduleSettle()
	if first == nil || second == nil {
		t.Fatal("data pane navigation should arm the debounce")
	}
	if cmd := v.Update(settleMsg{gen: v.settleGen - 1}); cmd != nil {
		t.Error("stale settle tick should be dropped")
	}
	// The current generation acts (no fetch here: nothing selected).
	v.Update(settleMsg{gen: v.settleGen})
	// Body pane: no debounce at all.
	v.pane = paneBody
	if v.scheduleSettle() != nil {
		t.Error("no debounce needed without a data pane")
	}
}

func TestThreadDoneRefetchesTargetPR(t *testing.T) {
	v := &View{pane: paneComments}
	v.list.SetRowHeight(2)
	a, b := mkPR("uA", "a", time.Hour), mkPR("uB", "b", time.Hour)
	v.raw = []pr{a, b}
	v.applySort()
	v.comments = map[string]*commentsState{"uA": {done: true}}
	// Selection sits on some other PR when the mutation on uA completes.
	v.list.Select(func(p pr) bool { return p.URL == "uB" })
	if cmd := v.Update(threadDoneMsg{url: "uA", what: "replied"}); cmd == nil {
		t.Fatal("completion should refetch the mutated PR")
	}
	st, ok := v.comments["uA"]
	if !ok || st.done {
		t.Error("uA's cache should be in flight again, keyed to uA not the selection")
	}
}
