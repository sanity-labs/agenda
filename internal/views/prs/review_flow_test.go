package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(r rune) tea.KeyPressMsg   { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func special(c rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: c} }

func startFlow(t *testing.T) *View {
	t.Helper()
	v := &View{}
	v.list.SetRowHeight(2)
	v.raw = []pr{mkPR("u1", "theirs", 0)}
	v.applySort()
	v.review = &reviewFlow{url: "u1", repo: "acme/repo", num: 1}
	return v
}

func TestReviewFlowVerdictThenBody(t *testing.T) {
	v := startFlow(t)
	v.updateReview(press('x'))
	if v.review.verdict != "request-changes" {
		t.Fatalf("verdict = %q, want request-changes", v.review.verdict)
	}
	// Empty body can't submit (GitHub rejects it).
	if cmd := v.updateReview(special(tea.KeyEnter)); cmd != nil {
		t.Error("empty body submitted")
	}
	for _, r := range "needs tests" {
		v.updateReview(press(r))
	}
	if v.review.body != "needs tests" {
		t.Fatalf("body = %q", v.review.body)
	}
	if cmd := v.updateReview(special(tea.KeyEnter)); cmd == nil {
		t.Error("valid body should submit")
	}
	if !v.review.submitting {
		t.Error("flow not marked submitting")
	}
	// Keys are ignored while gh runs.
	v.updateReview(press('z'))
	if v.review.body != "needs tests" {
		t.Error("keys leaked into a submitting flow")
	}
}

func TestReviewFlowApproveIsImmediate(t *testing.T) {
	v := startFlow(t)
	if cmd := v.updateReview(press('a')); cmd == nil {
		t.Error("a should submit an approval immediately")
	}
}

func TestReviewFlowEscape(t *testing.T) {
	v := startFlow(t)
	v.updateReview(press('c'))
	v.updateReview(press('h'))
	v.updateReview(special(tea.KeyEscape)) // body -> back to verdict picker
	if v.review == nil || v.review.verdict != "" || v.review.body != "" {
		t.Fatalf("esc from body should reset to verdict picker, got %+v", v.review)
	}
	v.updateReview(special(tea.KeyEscape)) // verdict picker -> cancel
	if v.review != nil {
		t.Error("esc from verdict picker should cancel the flow")
	}
}

func TestReviewDoneUpdatesFlash(t *testing.T) {
	v := startFlow(t)
	v.Update(reviewDoneMsg{what: "approved acme/repo#1"})
	if v.review != nil {
		t.Error("flow should close on completion")
	}
	if v.flash == "" {
		t.Error("no flash after a successful review")
	}
}

func TestReviewPopupCursorAndDiffOption(t *testing.T) {
	v := startFlow(t)
	// Cursor: down three times lands on "View diff"; enter activates it.
	for i := 0; i < 3; i++ {
		v.updateReview(press('j'))
	}
	if got := reviewOptions[v.review.sel].label; got != "View diff" {
		t.Fatalf("cursor on %q, want View diff", got)
	}
	if cmd := v.updateReview(special(tea.KeyEnter)); cmd == nil {
		t.Error("View diff should return a command (pager, diff pane off)")
	}
	if v.review != nil {
		t.Error("View diff should close the popup")
	}

	// With the diff pane enabled, the same option opens the review view
	// (the file list by default, the flat diff when review_view says so).
	v = startFlow(t)
	v.cfg.DiffPane = true
	v.updateReview(press('d'))
	if v.pane != paneFiles {
		t.Errorf("pane = %v after View diff with diff_pane on, want paneFiles", v.pane)
	}
	v = startFlow(t)
	v.cfg.DiffPane, v.cfg.ReviewView = true, "unified"
	v.updateReview(press('d'))
	if v.pane != paneDiff {
		t.Errorf("pane = %v after View diff with review_view unified, want paneDiff", v.pane)
	}
}

func TestReviewPopupOverlayRenders(t *testing.T) {
	v := startFlow(t)
	out := v.Overlay()
	for _, want := range []string{"Review acme/repo#1", "Approve", "Request changes", "View diff"} {
		if !strings.Contains(out, want) {
			t.Errorf("popup missing %q", want)
		}
	}
	v.review = nil
	if v.Overlay() != "" {
		t.Error("no flow: overlay should be empty")
	}
}
