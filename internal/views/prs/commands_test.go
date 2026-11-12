package prs

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func titles(v *View) string {
	var s []string
	for _, c := range v.Commands() {
		s = append(s, c.Title)
	}
	return strings.Join(s, "|")
}

func command(t *testing.T, v *View, id string) func() tea.Cmd {
	t.Helper()
	for _, c := range v.Commands() {
		if c.ID == id {
			return c.Run
		}
	}
	t.Fatalf("no command %q in %s", id, titles(v))
	return nil
}

func TestCommandsFollowTheSelectedPR(t *testing.T) {
	v := mergeView(t, mergeable(7))
	got := titles(v)
	for _, want := range []string{"Approve", "Merge", "Mark as draft", "Close with comment"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "Mark ready") {
		t.Errorf("a non-draft offers no ready: %s", got)
	}
	d := mergeable(8)
	d.IsDraft = true
	if got := titles(mergeView(t, d)); !strings.Contains(got, "Mark ready for review") || strings.Contains(got, "Mark as draft") {
		t.Errorf("a draft offers ready only: %s", got)
	}
}

func TestMergeCommandAsksFirst(t *testing.T) {
	v := mergeView(t, mergeable(7))
	command(t, v, "merge")()
	if v.review == nil || v.review.confirm != "merge" || v.review.submitting {
		t.Fatalf("merge should stage a confirmation, got %+v", v.review)
	}
}

func TestCloseCommandTakesCommentThenConfirms(t *testing.T) {
	v := mergeView(t, mergeable(7))
	command(t, v, "close")()
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // empty comment: stays
	if v.review.confirm != "" {
		t.Fatal("closed on an empty comment")
	}
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if v.review.confirm != "close" || v.review.submitting {
		t.Fatalf("close should wait for y, got %+v", v.review)
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd == nil || !v.review.submitting {
		t.Error("y should run the close")
	}
}

func TestRemoveReviewerCommandPerRequest(t *testing.T) {
	p := mergeable(7)
	if err := json.Unmarshal([]byte(`{"nodes":[
		{"requestedReviewer":{"login":"ana"}},
		{"requestedReviewer":{"combinedSlug":"acme/sre"}}]}`), &p.ReviewRequests); err != nil {
		t.Fatal(err)
	}
	v := mergeView(t, p)
	got := titles(v)
	for _, want := range []string{"Remove reviewer: ana", "Remove reviewer: acme/sre"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if cmd := command(t, v, "remove-reviewer-ana")(); cmd == nil || !v.review.submitting {
		t.Error("remove should run at once")
	}
	if strings.Contains(titles(mergeView(t, mergeable(8))), "Remove reviewer") {
		t.Error("no requests, no remove commands")
	}
}

func TestAddReviewerPromptsThenRuns(t *testing.T) {
	v := mergeView(t, mergeable(7))
	command(t, v, "add-reviewer")()
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if v.review.submitting {
		t.Fatal("ran on an empty login")
	}
	v.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || !v.review.submitting || v.review.confirm != "" {
		t.Fatalf("enter should run without a confirm, got %+v", v.review)
	}
	v = mergeView(t, mergeable(7))
	command(t, v, "add-reviewer")()
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if v.review != nil {
		t.Error("esc should cancel the popup")
	}
}
