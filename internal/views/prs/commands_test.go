package prs

import (
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
	for _, want := range []string{"Approve", "Merge", "Rebase", "Mark as draft", "Close with comment"} {
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

func TestRebaseCommandRunsGh(t *testing.T) {
	v := mergeView(t, mergeable(7))
	if cmd := command(t, v, "rebase")(); cmd == nil || !v.review.submitting {
		t.Error("rebase should start at once")
	}
}
