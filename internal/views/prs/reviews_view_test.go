package prs

import (
	"strings"
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/config"
)

func newReviewsView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return NewReviews(config.GitHubConfig{}, nil, nil, nil)
}

func TestReviewsViewIsItsOwnTab(t *testing.T) {
	v := newReviewsView(t)
	if got := v.Title(); got != "Reviews" {
		t.Errorf("Title() = %q, want Reviews", got)
	}
	if !v.showReview {
		t.Error("the review section is the whole tab, so it must be on regardless of show_review_requested")
	}
}

// The tab is review requests and nothing else: own PRs live on the PRs tab.
func TestReviewsViewListsOnlyReviewRequests(t *testing.T) {
	v := newReviewsView(t)
	v.Update(reviewListMsg{page: searchPage{prs: []pr{
		mkPR("u1", "theirs-old", 2*time.Hour),
		mkPR("u2", "theirs-new", time.Minute),
	}}})
	if v.Loading() {
		t.Error("still loading after the review search landed: the own-PR search never runs here")
	}
	if got := v.list.Total(); got != 2 {
		t.Fatalf("rows = %d, want the 2 review requests with no section band", got)
	}
	if v.list.Selected().Title != "theirs-new" {
		t.Errorf("selection = %q, want the newest review request", v.list.Selected().Title)
	}
	if s := v.statusText(); !strings.Contains(s, "2 to review") {
		t.Errorf("status = %q, want it to count review requests", s)
	}
}

// 'w' hides the review section on the PRs tab; here that would empty the tab.
func TestReviewsViewIgnoresSectionToggle(t *testing.T) {
	v := newReviewsView(t)
	v.Update(press('w'))
	if !v.showReview {
		t.Error("'w' turned the review section off in the Reviews tab")
	}
	for _, b := range v.Bindings() {
		if b.Help().Desc == "review reqs" {
			t.Error("the footer offers the 'w' toggle in the Reviews tab")
		}
	}
}

// There is only one search behind this tab, so 'F' edits it even before
// anything has loaded to put the cursor in the review section.
func TestReviewsViewEditsReviewFilter(t *testing.T) {
	v := newReviewsView(t)
	v.editFilter()
	if v.filterEd == nil || v.filterEd.path != "github.review_filter" {
		t.Errorf("filter editor = %+v, want it on github.review_filter", v.filterEd)
	}
}

// The two tabs keep separate caches: sharing one would let each overwrite
// the other's rows and unread marks on every fetch.
func TestReviewsViewCachesSeparately(t *testing.T) {
	v := newReviewsView(t)
	v.Update(reviewListMsg{page: searchPage{prs: []pr{mkPR("u1", "theirs", time.Hour)}}})
	if v := New(config.GitHubConfig{}, nil, nil, nil); len(v.reviewRaw) != 0 {
		t.Error("the PRs tab loaded the Reviews tab's cache")
	}
	if v := NewReviews(config.GitHubConfig{}, nil, nil, nil); len(v.reviewRaw) != 1 {
		t.Error("the Reviews tab did not restore its own cache")
	}
}
