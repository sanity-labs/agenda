package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func pagedView(t *testing.T, cfg config.GitHubConfig) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(cfg, nil, nil, nil)
	v.SetSize(80, 40, 20)
	return v
}

func TestDecodeSearchReadsPageInfo(t *testing.T) {
	body := `{"data":{"search":{"issueCount":79,
	 "pageInfo":{"hasNextPage":true,"endCursor":"Y3Vyc29yOjIw"},
	 "nodes":[{"number":1},{"number":2}]}}}`
	page, err, ok := decodeSearch([]byte(body))
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if page.total != 79 || !page.hasMore || page.cursor != "Y3Vyc29yOjIw" {
		t.Errorf("page = %+v, want total 79, hasMore, and the cursor", page)
	}
}

// A second page appends; it must not replace what is already loaded.
func TestLaterPageAppends(t *testing.T) {
	v := pagedView(t, config.GitHubConfig{})
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1}}, total: 3, cursor: "c1", hasMore: true}})
	if len(v.raw) != 1 {
		t.Fatalf("first page left %d rows, want 1", len(v.raw))
	}
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 2}}, total: 3}, more: true})
	if len(v.raw) != 2 {
		t.Errorf("after the second page: %d rows, want 2 (appended)", len(v.raw))
	}
	if v.minePage.hasMore {
		t.Error("hasMore still set after the last page: it would keep fetching")
	}
}

// The header has to admit when the list is partial, or 20 of 79 looks like 79.
func TestHeaderShowsPartialCount(t *testing.T) {
	v := pagedView(t, config.GitHubConfig{})
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1}, {Number: 2}}, total: 79, cursor: "c", hasMore: true}})
	if got := v.statusText(); !strings.Contains(got, "2 of 79") {
		t.Errorf("status = %q, want it to say 2 of 79", got)
	}
	// Fully loaded reads as a plain count.
	v2 := pagedView(t, config.GitHubConfig{})
	v2.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1}}, total: 1}})
	if got := v2.statusText(); strings.Contains(got, " of ") {
		t.Errorf("status = %q, want no of-total when fully loaded", got)
	}
}

func TestFetchMoreStopsWhenExhausted(t *testing.T) {
	v := pagedView(t, config.GitHubConfig{})
	v.minePage = pageState{hasMore: false}
	if cmd := v.fetchMore(); cmd != nil {
		t.Error("fetchMore returned a command with nothing left to fetch")
	}
	// In flight already: do not stack a second request for the same page.
	v.minePage = pageState{hasMore: true, loading: true}
	if cmd := v.fetchMore(); cmd != nil {
		t.Error("fetchMore fired while a page was already loading")
	}
}

func TestLazyPagingOffFetchesEverything(t *testing.T) {
	off := false
	v := pagedView(t, config.GitHubConfig{LazyPaging: &off})
	if got := v.pageSize(); got != 100 {
		t.Errorf("page size with lazy paging off = %d, want the API ceiling of 100", got)
	}
	v.minePage = pageState{hasMore: true}
	if cmd := v.fetchMore(); cmd != nil {
		t.Error("fetchMore fired with lazy paging off")
	}
}

func TestPageSizeConfig(t *testing.T) {
	v := pagedView(t, config.GitHubConfig{PageSize: 50})
	if got := v.pageSize(); got != 50 {
		t.Errorf("page size = %d, want the configured 50", got)
	}
	// Absent, too small, and too large all resolve to something usable.
	for in, want := range map[int]int{0: 20, -5: 20, 500: 100} {
		if got := (config.GitHubConfig{PageSize: in}).ResolvedPageSize(); got != want {
			t.Errorf("ResolvedPageSize(%d) = %d, want %d", in, got, want)
		}
	}
}

// A later page is all new by definition; notifying for each row would mean a
// notification storm just from scrolling.
func TestLaterReviewPageDoesNotNotify(t *testing.T) {
	v := pagedView(t, config.GitHubConfig{})
	v.notifier = stubNotifier{}
	v.seeded = true
	v.reviewRaw = []pr{{Number: 1, URL: "u1"}}
	cmd := v.Update(reviewListMsg{page: searchPage{prs: []pr{{Number: 2, URL: "u2"}}}, more: true})
	if cmd != nil {
		if _, isToast := cmd().(ui.ToastMsg); isToast {
			t.Error("a paged-in review notified; only a first page should")
		}
	}
}

type stubNotifier struct{}

func (stubNotifier) Notify(title, body, _ string) tea.Msg {
	return ui.ToastMsg{Title: title, Body: body}
}
