package prs

import (
	tea "charm.land/bubbletea/v2"

	"errors"
	"strings"
	"testing"
	"time"
)

func mkPR(url, title string, age time.Duration) pr {
	p := pr{Title: title, URL: url, UpdatedAt: time.Now().Add(-age)}
	p.Repository.NameWithOwner = "acme/repo"
	p.Number = 1
	return p
}

func TestApplySortBuildsReviewSection(t *testing.T) {
	v := &View{showReview: true}
	v.list.SetRowHeight(2)
	v.raw = []pr{mkPR("u1", "mine-old", 2*time.Hour), mkPR("u2", "mine-new", time.Minute)}
	v.reviewRaw = []pr{mkPR("u3", "theirs", time.Hour)}
	v.applySort()

	if got := v.list.Total(); got != 5 {
		t.Fatalf("Total = %d, want a band per section + 2 mine + 1 review", got)
	}
	// Two bands, each naming and counting its own section.
	var bands []string
	for _, p := range v.list.Items() {
		if p.Separator != "" {
			bands = append(bands, p.Separator)
		}
	}
	want := []string{"MY PULL REQUESTS  ·  2", "REVIEW REQUESTED  ·  1"}
	if len(bands) != 2 || bands[0] != want[0] || bands[1] != want[1] {
		t.Errorf("bands = %v, want %v", bands, want)
	}
	if v.list.Selected().Title != "mine-new" {
		t.Errorf("first selection = %q, want the newest own PR", v.list.Selected().Title)
	}

	// Toggled off, the review section disappears entirely.
	v.showReview = false
	v.applySort()
	if got := v.list.Total(); got != 2 {
		t.Errorf("Total after toggle = %d, want 2", got)
	}

	// No review PRs: no dangling separator, and with only one section left
	// no bands either.
	v.showReview, v.reviewRaw = true, nil
	v.applySort()
	if v.list.Any(func(p pr) bool { return p.Separator != "" }) {
		t.Error("band rendered with an empty review section")
	}
}

// A band earns its two rows only by telling the sections apart, so a list
// that holds just one section stays flat.
func TestSingleSectionRendersNoBands(t *testing.T) {
	mine := []pr{mkPR("u1", "mine", time.Hour)}
	theirs := []pr{mkPR("u2", "theirs", time.Hour)}
	cases := []struct {
		name       string
		mine, rev  []pr
		showReview bool
		wantBands  int
	}{
		{"own PRs only, toggle off", mine, theirs, false, 0},
		{"own PRs only, empty review", mine, nil, true, 0},
		{"review only, no own PRs", nil, theirs, true, 0},
		{"both sections", mine, theirs, true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := &View{showReview: c.showReview}
			v.list.SetRowHeight(2)
			v.raw, v.reviewRaw = c.mine, c.rev
			v.applySort()
			bands := 0
			for _, p := range v.list.Items() {
				if p.Separator != "" {
					bands++
				}
			}
			if bands != c.wantBands {
				t.Errorf("bands = %d, want %d", bands, c.wantBands)
			}
			if got, want := v.list.Total(), len(c.mine)+len(c.rev)+c.wantBands; c.showReview && got != want {
				t.Errorf("rows = %d, want %d", got, want)
			}
		})
	}
}

// An empty own-PR list still has to surface a failed review fetch, since the
// band is the only place that error is reported.
func TestReviewFetchErrorAlwaysBanded(t *testing.T) {
	v := &View{showReview: true, reviewErr: errors.New("boom")}
	v.list.SetRowHeight(2)
	v.applySort()
	var bands []string
	for _, p := range v.list.Items() {
		if p.Separator != "" {
			bands = append(bands, p.Separator)
		}
	}
	if len(bands) != 1 || !strings.Contains(bands[0], "fetch failed") {
		t.Errorf("bands = %v, want one reporting the failed fetch", bands)
	}
}

func TestNotifyNewReviewsGating(t *testing.T) {
	v := &View{}
	if cmd := v.notifyNewReviews(nil, []pr{mkPR("u1", "t", 0)}); cmd != nil {
		t.Error("no notifier: expected nil command")
	}
	v.notifier = fakeNotifier{}
	if cmd := v.notifyNewReviews(nil, []pr{mkPR("u1", "t", 0)}); cmd != nil {
		t.Error("unseeded: expected nil command")
	}
	v.seeded = true
	if cmd := v.notifyNewReviews([]pr{mkPR("u1", "t", 0)}, []pr{mkPR("u1", "t", 0), mkPR("u2", "t2", 0)}); cmd == nil {
		t.Error("new review request: expected a command")
	}
	if cmd := v.notifyNewReviews([]pr{mkPR("u1", "t", 0)}, []pr{mkPR("u1", "t", 0)}); cmd != nil {
		t.Error("unchanged set: expected nil command")
	}
}

type fakeNotifier struct{}

func (fakeNotifier) Notify(title, body string) tea.Msg { return nil }
