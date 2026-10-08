package prs

import (
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

func previewView(t *testing.T, cfg config.GitHubConfig) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(cfg, nil, nil, nil)
	v.SetSize(80, 60, 40)
	return v
}

func TestTruncateSummary(t *testing.T) {
	body := strings.Repeat("kept\n", 30)
	out := truncateSummary(body, 10, "e")
	if got := strings.Count(out, "kept"); got != 10 {
		t.Errorf("kept %d lines, want 10", got)
	}
	// The hint names both affordances: the key and the click.
	for _, want := range []string{"20 more lines", "e", expandMarker} {
		if !strings.Contains(out, want) {
			t.Errorf("hint is missing %q:\n%s", want, out)
		}
	}
}

// A description that already fits must not gain a hint about hidden lines.
func TestTruncateSummaryLeavesShortBodies(t *testing.T) {
	body := "one\ntwo\nthree"
	if got := truncateSummary(body, 10, "e"); got != body {
		t.Errorf("short body changed:\n%q", got)
	}
}

func TestChecksBlockReportsCounts(t *testing.T) {
	v := previewView(t, config.GitHubConfig{})
	p := pr{Number: 1, URL: "u"}
	p.Commits.Nodes = []commitNode{{}}
	cx := &p.Commits.Nodes[0].Commit.StatusCheckRollup
	cx.State = "SUCCESS"
	cx.Contexts.TotalCount = 8
	cx.Contexts.CheckRunCountsByState = []contextCount{{State: "SUCCESS", Count: 8}}

	out := v.checksBlock(p)
	if !strings.Contains(out, "All checks have passed") || !strings.Contains(out, "8 successful") {
		t.Errorf("checks block:\n%s", out)
	}
}

func TestChecksBlockReportsFailures(t *testing.T) {
	v := previewView(t, config.GitHubConfig{})
	p := pr{Number: 1, URL: "u", Mergeable: "CONFLICTING"}
	p.Commits.Nodes = []commitNode{{}}
	cx := &p.Commits.Nodes[0].Commit.StatusCheckRollup
	cx.State = "FAILURE"
	cx.Contexts.TotalCount = 9
	cx.Contexts.CheckRunCountsByState = []contextCount{
		{State: "SUCCESS", Count: 7}, {State: "FAILURE", Count: 2},
	}

	out := v.checksBlock(p)
	for _, want := range []string{"2 of 9 checks failed", "7 passed", "Merging is blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("checks block missing %q:\n%s", want, out)
		}
	}
}

// "waiting on a reviewer" names the pending requests when there are any:
// people as @login, teams as @org/team.
func TestChecksBlockNamesRequestedReviewers(t *testing.T) {
	v := previewView(t, config.GitHubConfig{})
	p := pr{Number: 1, URL: "u", ReviewDecision: "REVIEW_REQUIRED"}
	if got := v.checksBlock(p); !strings.Contains(got, "waiting on a reviewer") {
		t.Errorf("no requests:\n%s", got)
	}
	var user, team struct {
		RequestedReviewer struct {
			Login        string `json:"login"`
			CombinedSlug string `json:"combinedSlug"`
		} `json:"requestedReviewer"`
	}
	user.RequestedReviewer.Login = "alice"
	team.RequestedReviewer.CombinedSlug = "o/sre"
	p.ReviewRequests.Nodes = append(p.ReviewRequests.Nodes, user, team)
	got := v.checksBlock(p)
	if !strings.Contains(got, "waiting on @alice, @o/sre") || strings.Contains(got, "a reviewer") {
		t.Errorf("with requests:\n%s", got)
	}
}

// The decision says who holds it: approvers under Approved, the
// requester under Changes requested.
func TestChecksBlockNamesReviewers(t *testing.T) {
	v := previewView(t, config.GitHubConfig{})
	p := pr{Number: 1, URL: "u", ReviewDecision: "APPROVED"}
	if got := v.checksBlock(p); strings.Contains(got, " by ") {
		t.Errorf("nobody known, yet:\n%s", got)
	}
	add := func(login, state string) {
		var n struct {
			State  string `json:"state"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
		}
		n.State, n.Author.Login = state, login
		p.LatestOpinionatedReviews.Nodes = append(p.LatestOpinionatedReviews.Nodes, n)
	}
	add("alice", "APPROVED")
	add("bob", "APPROVED")
	add("carol", "CHANGES_REQUESTED")
	if got := v.checksBlock(p); !strings.Contains(got, "by @alice, @bob") || strings.Contains(got, "carol") {
		t.Errorf("approved:\n%s", got)
	}
	p.ReviewDecision = "CHANGES_REQUESTED"
	if got := v.checksBlock(p); !strings.Contains(got, "by @carol") || strings.Contains(got, "alice") {
		t.Errorf("changes requested:\n%s", got)
	}
}

// Nothing to report means no empty box.
func TestChecksBlockEmptyWhenNothingToSay(t *testing.T) {
	v := previewView(t, config.GitHubConfig{})
	if got := v.checksBlock(pr{Number: 1, URL: "u"}); got != "" {
		t.Errorf("checks block = %q, want empty for a PR with no checks or review", got)
	}
}

func TestCommentsBlock(t *testing.T) {
	v := previewView(t, config.GitHubConfig{})
	p := pr{Number: 1, URL: "u"}
	if got := v.commentsBlock(p); !strings.Contains(got, "none yet") {
		t.Errorf("no-comments block = %q", got)
	}
	p.Comments.TotalCount = 4
	got := v.commentsBlock(p)
	if !strings.Contains(got, "4") || !strings.Contains(got, "to toggle") {
		t.Errorf("comments block = %q, want the count and the toggle key", got)
	}
}

// 'e' toggles per PR and must not leak to the next row.
func TestExpandIsPerPR(t *testing.T) {
	v := previewView(t, config.GitHubConfig{SummaryLines: 5})
	long := strings.Repeat("a line of description\n", 40)
	v.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1", Body: long},
		{Number: 2, URL: "u2", Body: long},
	}}})

	first := v.renderedBody(v.list.Selected())
	if !strings.Contains(first, "more lines") {
		t.Fatalf("a long body was not truncated:\n%s", first)
	}
	v.expanded = "u1"
	v.bodyKey = ""
	if strings.Contains(v.renderedBody(v.list.Selected()), "more lines") {
		t.Error("expanding did not show the whole body")
	}
	// A different PR is still truncated.
	v.bodyKey = ""
	other := v.renderedBody(pr{Number: 2, URL: "u2", Body: long})
	if !strings.Contains(other, "more lines") {
		t.Error("expansion leaked to another PR")
	}
}

// The hint has to survive expansion, or there is no way back. Uses
// paragraphs rather than repeated short lines: glamour reflows those into
// fewer lines than you wrote, so nothing gets truncated.
func TestExpandedShowsCollapseHint(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{SummaryLines: 5}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	p := pr{Number: 1, URL: "u1", Body: strings.Repeat("A paragraph of description text.\n\n", 40)}

	collapsed := v.renderedBody(p)
	if !strings.Contains(collapsed, "to toggle") {
		t.Errorf("collapsed body has no hint:\n%s", collapsed)
	}
	v.expanded, v.bodyKey = "u1", ""
	expanded := v.renderedBody(p)
	if !strings.Contains(expanded, "to toggle") {
		t.Errorf("expanded body has no way back:\n%s", expanded[len(expanded)-200:])
	}
}
