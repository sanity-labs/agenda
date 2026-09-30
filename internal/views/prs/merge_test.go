package prs

import (
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

// mergeView is a view with one PR loaded and the merge action enabled.
func mergeView(t *testing.T, p pr) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{Merge: true}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	return v
}

func mergeable(num int) pr {
	p := pr{Number: num, URL: "u", Title: "a thing", Mergeable: "MERGEABLE"}
	p.Repository.NameWithOwner = "o/r"
	return p
}

// Merging is irreversible, so it never happens on the keypress that
// selects it: the popup asks first and only "y" goes through.
func TestMergeAsksBeforeMerging(t *testing.T) {
	v := mergeView(t, mergeable(7))
	v.Update(tea.KeyPressMsg{Code: 'r'}) // open the popup
	if v.review == nil {
		t.Fatal("'r' did not open the review popup")
	}
	v.Update(tea.KeyPressMsg{Code: 'm'})
	if v.review.confirm != "merge" {
		t.Fatalf("confirm = %q, want the merge staged for confirmation", v.review.confirm)
	}
	if v.review.submitting {
		t.Error("the merge ran before it was confirmed")
	}
	overlay := v.Overlay()
	for _, want := range []string{"o/r#7", "a thing", "squash", "y to confirm"} {
		if !strings.Contains(overlay, want) {
			t.Errorf("confirmation is missing %q:\n%s", want, overlay)
		}
	}
}

// Any key but 'y' backs out, enter included: enter is what selected the
// entry, so treating it as consent would merge on a single keypress.
func TestAnythingButYCancelsTheMerge(t *testing.T) {
	for _, key := range []rune{'n', '\r', 'q', 'j'} {
		v := mergeView(t, mergeable(7))
		v.Update(tea.KeyPressMsg{Code: 'r'})
		v.Update(tea.KeyPressMsg{Code: 'm'})
		v.Update(tea.KeyPressMsg{Code: key})
		if v.review == nil {
			t.Fatalf("%q closed the whole popup", key)
		}
		if v.review.confirm != "" || v.review.submitting {
			t.Errorf("%q did not cancel: confirm=%q submitting=%v",
				key, v.review.confirm, v.review.submitting)
		}
	}
}

// A PR GitHub already says cannot land is refused with the reason, rather
// than shelling out and failing afterwards.
func TestMergeRefusesUnmergeablePRs(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*pr)
		want string
	}{
		{"draft", func(p *pr) { p.IsDraft = true }, "draft"},
		{"conflicts", func(p *pr) { p.Mergeable = "CONFLICTING" }, "conflicts"},
		{"unknown", func(p *pr) { p.Mergeable = "UNKNOWN" }, "mergeability"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mergeable(7)
			c.mut(&p)
			v := mergeView(t, p)
			v.Update(tea.KeyPressMsg{Code: 'r'})
			v.Update(tea.KeyPressMsg{Code: 'm'})
			if v.review.confirm != "" {
				t.Errorf("staged a merge for an unmergeable PR: %q", v.review.confirm)
			}
			if !strings.Contains(v.review.blocked, c.want) {
				t.Errorf("blocked = %q, want it to mention %q", v.review.blocked, c.want)
			}
		})
	}
}

// Auto-merge is for PRs that are not mergeable yet, so a pending
// mergeability check must not block it.
func TestAutoMergeAllowsPendingMergeability(t *testing.T) {
	p := mergeable(7)
	p.Mergeable = "UNKNOWN"
	v := mergeView(t, p)
	v.Update(tea.KeyPressMsg{Code: 'r'})
	v.Update(tea.KeyPressMsg{Code: 'M'})
	if v.review.confirm != "auto" {
		t.Errorf("confirm = %q (blocked: %q), want auto-merge staged",
			v.review.confirm, v.review.blocked)
	}
}

// Unapproved or failing is a judgement call, not a refusal: the repo's own
// rules gate the merge, so agenda says what is off and asks.
func TestMergeWarnsButStillAsks(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*pr)
		want string
	}{
		{"changes requested", func(p *pr) { p.ReviewDecision = "CHANGES_REQUESTED" }, "changes"},
		{"not approved", func(p *pr) { p.ReviewDecision = "REVIEW_REQUIRED" }, "approved"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mergeable(7)
			c.mut(&p)
			v := mergeView(t, p)
			v.Update(tea.KeyPressMsg{Code: 'r'})
			v.Update(tea.KeyPressMsg{Code: 'm'})
			if v.review.confirm != "merge" {
				t.Fatalf("refused instead of warning: blocked = %q", v.review.blocked)
			}
			if !strings.Contains(v.review.warn, c.want) {
				t.Errorf("warn = %q, want it to mention %q", v.review.warn, c.want)
			}
		})
	}
}

// The merge entries are opt-in: without github.merge there is nothing in
// the popup to press, and 'm' does nothing.
func TestMergeEntriesAreOptIn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil) // merge off
	v.SetSize(80, 60, 40)
	v.Update(mineMsg{page: searchPage{prs: []pr{mergeable(7)}}})
	v.Update(tea.KeyPressMsg{Code: 'r'})

	for _, opt := range v.options() {
		if opt.label == mergeLabel || opt.label == autoLabel {
			t.Fatalf("merge entry %q offered with github.merge off", opt.label)
		}
	}
	v.Update(tea.KeyPressMsg{Code: 'm'})
	if v.review.confirm != "" {
		t.Errorf("'m' staged a merge with the feature off: %q", v.review.confirm)
	}
}

// 'd' and Cancel sit after the merge entries so a mistimed keypress cannot
// land on one, and the review verdicts keep their original hotkeys.
func TestMergeEntriesDoNotDisplaceTheVerdicts(t *testing.T) {
	v := mergeView(t, mergeable(7))
	opts := v.options()
	for i, want := range []string{"a", "c", "x", "m", "M", "d", ""} {
		if opts[i].key != want {
			t.Errorf("option %d key = %q, want %q", i, opts[i].key, want)
		}
	}
}

// Approving a PR you already approved asks first: the whole point is
// catching the case where you forgot you had.
func TestSecondApprovalAsksFirst(t *testing.T) {
	p := mergeable(7)
	p.ViewerLatestReview.State = "APPROVED"
	v := mergeView(t, p)
	v.Update(tea.KeyPressMsg{Code: 'r'})
	v.Update(tea.KeyPressMsg{Code: 'a'})

	if v.review.confirm != "approve" {
		t.Fatalf("confirm = %q, want the second approval staged", v.review.confirm)
	}
	if v.review.submitting {
		t.Error("it approved again without asking")
	}
	if !strings.Contains(v.review.warn, "already approved") {
		t.Errorf("warn = %q, want it to say the PR is already approved", v.review.warn)
	}
	// The merge method is meaningless here and must not appear.
	if overlay := v.Overlay(); strings.Contains(overlay, "squash") {
		t.Errorf("the approval confirmation mentions a merge method:\n%s", overlay)
	}
}

// A first approval is not second-guessed: the guard must not add a
// keystroke to the common case.
func TestFirstApprovalDoesNotAsk(t *testing.T) {
	v := mergeView(t, mergeable(7)) // no viewer review
	v.Update(tea.KeyPressMsg{Code: 'r'})
	v.Update(tea.KeyPressMsg{Code: 'a'})
	if v.review.confirm != "" {
		t.Errorf("confirm = %q, want a first approval to go straight through", v.review.confirm)
	}
	if !v.review.submitting {
		t.Error("the first approval did not submit")
	}
}

// hide_approved is about PRs approved by anyone and still open: an
// approval from someone else does not mean you are done with it.
func TestHideApprovedFiltersTheReviewList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{HideApproved: true, ShowReviewRequested: boolPtr(true)}, nil, nil, nil)
	v.SetSize(80, 60, 40)

	rows := []pr{
		reviewRow("u1", 1, "APPROVED", "OPEN"),          // hidden
		reviewRow("u2", 2, "REVIEW_REQUIRED", "OPEN"),   // needs a review
		reviewRow("u3", 3, "CHANGES_REQUESTED", "OPEN"), // needs work
		reviewRow("u4", 4, "", "OPEN"),                  // no decision yet
	}
	v.Update(reviewListMsg{page: searchPage{prs: rows}})

	shown := listedURLs(v)
	if shown["u1"] {
		t.Errorf("an approved, open PR is still listed: %v", keys(shown))
	}
	for _, want := range []string{"u2", "u3", "u4"} {
		if !shown[want] {
			t.Errorf("%s was hidden but is not approved: %v", want, keys(shown))
		}
	}

	// Hiding is a view filter, not a fetch filter, so toggling it back on
	// needs no refetch.
	if len(v.reviewRaw) != len(rows) {
		t.Errorf("reviewRaw = %d rows, want all %d kept", len(v.reviewRaw), len(rows))
	}
}

// The viewer's own review is not what decides it: a PR someone else
// approved is hidden too, and one the viewer merely commented on is not.
func TestHideApprovedIgnoresWhoApproved(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{HideApproved: true, ShowReviewRequested: boolPtr(true)}, nil, nil, nil)
	v.SetSize(80, 60, 40)

	byOther := reviewRow("u1", 1, "APPROVED", "OPEN") // no viewer review at all
	commented := reviewRow("u2", 2, "REVIEW_REQUIRED", "OPEN")
	commented.ViewerLatestReview.State = "COMMENTED"
	v.Update(reviewListMsg{page: searchPage{prs: []pr{byOther, commented}}})

	shown := listedURLs(v)
	if shown["u1"] {
		t.Error("a PR approved by someone else was left visible")
	}
	if !shown["u2"] {
		t.Error("a PR the viewer only commented on was hidden")
	}
}

// A merged PR is not what this toggle resurrects: the default filter is
// is:open, and widening it should not bring merged work back.
func TestHideApprovedLeavesMergedPRsToTheFilter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{HideApproved: true, ShowReviewRequested: boolPtr(true)}, nil, nil, nil)
	v.SetSize(80, 60, 40)

	merged := reviewRow("u1", 1, "APPROVED", "MERGED")
	v.Update(reviewListMsg{page: searchPage{prs: []pr{merged}}})
	if !listedURLs(v)["u1"] {
		t.Error("hide_approved hid a merged PR; that is the search filter's job")
	}
}

// Off by default, the list shows what it always did.
func TestApprovedShownByDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{ShowReviewRequested: boolPtr(true)}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	v.Update(reviewListMsg{page: searchPage{prs: []pr{reviewRow("u1", 1, "APPROVED", "OPEN")}}})
	if !listedURLs(v)["u1"] {
		t.Error("an approved PR was hidden with hide_approved off")
	}
}

func reviewRow(url string, num int, decision, state string) pr {
	p := pr{Number: num, URL: url, Title: "t", Mergeable: "MERGEABLE",
		ReviewDecision: decision, State: state}
	p.Repository.NameWithOwner = "o/r"
	return p
}

func listedURLs(v *View) map[string]bool {
	out := map[string]bool{}
	for _, p := range v.list.Items() {
		if p.URL != "" {
			out[p.URL] = true
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolPtr(b bool) *bool { return &b }
