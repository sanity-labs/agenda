package prs

import (
	tea "charm.land/bubbletea/v2"

	"strings"
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/cache"
	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
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
	if got := v.list.Total(); got != 3 {
		t.Fatalf("rows = %d, want the review band + 2 review requests", got)
	}
	if band := v.list.Items()[0]; !strings.HasPrefix(band.Separator, "REVIEW REQUESTED") {
		t.Errorf("the tab does not lead with its band: %q", band.Separator)
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

// With a Reviews tab beside it, the PRs tab hands the review section over:
// the section is off, 'w' explains rather than toggling, and the footer
// does not offer it. One owner for the search, the cache and the marks.
func TestPRsTabDelegatesReviewsToTheReviewsTab(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// A cache from before the handoff: own rows, review rows, marks on both.
	if err := cache.Save("prs", cachedPRs{
		Mine:   []pr{{Number: 1, URL: "u1", Title: "mine", State: "OPEN"}},
		Review: []pr{{Number: 2, URL: "u2", Title: "theirs", State: "OPEN"}},
		Unread: []string{"u1", "u2"},
	}); err != nil {
		t.Fatal(err)
	}
	on := true
	v := New(config.GitHubConfig{ShowReviewRequested: &on}, nil, nil, nil).DelegateReviews()
	if v.showReview {
		t.Error("the review section is still on with a Reviews tab owning it")
	}
	if len(v.reviewRaw) != 0 || v.unread["u2"] {
		t.Errorf("the PRs tab kept the review rows or their marks it handed over: rows=%d unread=%v", len(v.reviewRaw), v.unread)
	}
	if len(v.raw) != 1 || !v.unread["u1"] {
		t.Errorf("the handoff touched the PRs tab's own rows or marks: rows=%d unread=%v", len(v.raw), v.unread)
	}
	cmd := v.Update(press('w'))
	if v.showReview {
		t.Error("'w' turned the delegated review section back on")
	}
	if cmd == nil {
		t.Fatal("'w' was silent on the delegating PRs tab")
	}
	if _, ok := cmd().(ui.ToastMsg); !ok {
		t.Error("'w' did not explain itself with a toast")
	}
	for _, b := range v.Bindings() {
		if b.Help().Desc == "review reqs" {
			t.Error("the footer offers 'w' on the delegating PRs tab")
		}
	}
}

// 'w' in the Reviews tab says why it does nothing instead of doing nothing.
func TestReviewsViewExplainsSectionToggle(t *testing.T) {
	v := newReviewsView(t)
	cmd := v.Update(press('w'))
	if cmd == nil {
		t.Fatal("'w' was silent in the Reviews tab")
	}
	if _, ok := cmd().(ui.ToastMsg); !ok {
		t.Error("'w' did not explain itself with a toast")
	}
}

// The root model broadcasts data messages to every view, and both tabs are
// this type. A search result stays with the tab that ran it: without this
// the Reviews tab showed the PRs tab's own PRs, which is exactly the list
// it exists to leave out.
func TestListResultsStayWithTheirTab(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	own := New(config.GitHubConfig{}, nil, nil, nil)
	rev := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	mine := mineMsg{from: own, page: searchPage{prs: []pr{{Number: 1, URL: "u1", Title: "mine", State: "OPEN"}}}}
	theirs := reviewListMsg{from: rev, page: searchPage{prs: []pr{{Number: 2, URL: "u2", Title: "theirs", State: "OPEN"}}}}
	for _, v := range []*View{own, rev} {
		v.Update(mine)
		v.Update(theirs)
	}
	if len(rev.raw) != 0 {
		t.Errorf("the Reviews tab took the PRs tab's own PRs: %d rows", len(rev.raw))
	}
	if len(own.reviewRaw) != 0 {
		t.Errorf("the PRs tab took the Reviews tab's results: %d rows", len(own.reviewRaw))
	}
	if len(own.raw) != 1 || len(rev.reviewRaw) != 1 {
		t.Errorf("each tab should keep its own result: own=%d rev=%d", len(own.raw), len(rev.reviewRaw))
	}
}

// A run of the broadcast bug wrote own PRs into the reviews cache. The tab
// never fetches own PRs, so a stale cached row would otherwise outlive
// every refresh: cached own rows are dropped on load and never written.
func TestReviewsTabIgnoresCachedOwnPRs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stale := cachedPRs{
		Mine:   []pr{{Number: 1, URL: "u1", Title: "mine", State: "OPEN"}},
		Review: []pr{{Number: 2, URL: "u2", Title: "theirs", State: "OPEN"}},
	}
	if err := cache.Save("reviews", stale); err != nil {
		t.Fatal(err)
	}
	v := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	if len(v.raw) != 0 {
		t.Errorf("the Reviews tab loaded %d own PRs from the cache", len(v.raw))
	}
	if len(v.reviewRaw) != 1 {
		t.Errorf("the Reviews tab lost its cached review requests: %d", len(v.reviewRaw))
	}
	v.raw = stale.Mine // however they get in
	v.applySort()
	for _, p := range v.list.Items() {
		if p.URL == "u1" {
			t.Error("an own PR rendered in the Reviews tab")
		}
	}
	v.saveCache()
	saved, _ := cache.Load[cachedPRs]("reviews")
	if len(saved.Mine) != 0 {
		t.Errorf("the Reviews tab wrote %d own PRs back to its cache", len(saved.Mine))
	}
}

// Beyond list results, the other tab's review verdicts, merges and filter
// trials stay with it: they would otherwise close this tab's popup, flash
// here, and refetch on a filter this tab is not editing.
func TestTabActionsStayWithTheirTab(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	own := New(config.GitHubConfig{}, nil, nil, nil)
	rev := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	rev.review = &reviewFlow{url: "u2"}
	for _, msg := range []tea.Msg{
		reviewDoneMsg{from: own, url: "u1", what: "approved", state: "APPROVED"},
		mergeDoneMsg{from: own, url: "u1", what: "merged"},
		filterTriedMsg{from: own, path: "github.filter", query: "is:open", got: 3},
	} {
		if cmd := rev.Update(msg); cmd != nil {
			t.Errorf("%T from the PRs tab made the Reviews tab act", msg)
		}
	}
	if rev.flash != "" {
		t.Errorf("the PRs tab's result flashed in the Reviews tab: %q", rev.flash)
	}
	if rev.review == nil {
		t.Error("the PRs tab's verdict closed the Reviews tab's review popup")
	}
}

// Shared caches reach both tabs, and the one that never fetched has no map
// for them: with the Reviews tab on, 'r' (which opens the diff) crashed on
// a nil-map write in the other tab. Results a tab did not ask for are
// ignored, and thread actions stay with the tab that ran them.
func TestOtherTabsResultsDoNotPanicOrLeak(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	own := New(config.GitHubConfig{}, nil, nil, nil)
	rev := NewReviews(config.GitHubConfig{}, nil, nil, nil) // never fetched anything
	rev.Update(diffMsg{url: "u1", text: "+x"})
	if rev.diffs != nil {
		t.Error("the Reviews tab cached a diff it never asked for")
	}
	rev.input = &threadFlow{}
	rev.Update(threadDoneMsg{from: own, what: "replied"})
	if rev.input == nil || rev.flash != "" {
		t.Errorf("the PRs tab's thread action closed or flashed the Reviews tab: input=%v flash=%q", rev.input != nil, rev.flash)
	}
}
