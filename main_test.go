package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

type stubNotifier struct{}

func (stubNotifier) Notify(_, _, _ string) tea.Msg { return nil }

// Both tabs watch the same review-requested search, so with both on, one
// review request would otherwise pop two notifications.
func TestReviewsTabOwnsReviewNotifications(t *testing.T) {
	n := stubNotifier{}
	if got := prsNotifier([]string{"prs", "linear"}, n); got == nil {
		t.Error("without a Reviews tab, the PRs tab must keep notifying")
	}
	if got := prsNotifier([]string{"reviews", "prs"}, n); got != nil {
		t.Error("with a Reviews tab, the PRs tab notified too")
	}
	if got := prsNotifier([]string{"prs"}, nil); got != nil {
		t.Error("notifications off must stay off")
	}
}
