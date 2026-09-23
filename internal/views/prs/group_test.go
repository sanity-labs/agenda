package prs

import (
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/ui"
)

func TestGroupingWithReviewSection(t *testing.T) {
	v := &View{showReview: true, grouping: true, sort: sortRepo}
	v.list.SetRowHeight(2)
	a := mkPR("u1", "mine-a", time.Hour)
	a.Repository.NameWithOwner = "acme/alpha"
	b := mkPR("u2", "mine-b", time.Hour)
	b.Repository.NameWithOwner = "acme/beta"
	r := mkPR("u3", "theirs", time.Hour)
	r.Repository.NameWithOwner = "acme/alpha"
	v.raw, v.reviewRaw = []pr{a, b}, []pr{r}
	v.applySort()

	// A band per section, each with its count, and repo lanes nested under
	// them: the bands mark the two lists, the lanes subdivide each one.
	var got []string
	for _, p := range v.list.Items() {
		switch {
		case p.Separator != "" && p.Group:
			got = append(got, "lane:"+p.Separator)
		case p.Separator != "":
			got = append(got, "section:"+p.Separator)
		default:
			got = append(got, p.Title)
		}
	}
	want := []string{
		"section:MY PULL REQUESTS  ·  2",
		"lane:acme/alpha", "mine-a",
		"lane:acme/beta", "mine-b",
		"section:REVIEW REQUESTED  ·  1",
		"lane:acme/alpha", "theirs",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestGroupingOffOrUngroupableSortStaysFlat(t *testing.T) {
	v := &View{}
	v.list.SetRowHeight(2)
	v.raw = []pr{mkPR("u1", "one", time.Hour)}
	v.applySort()
	if v.list.Total() != 1 {
		t.Errorf("grouping off: want flat list, got %d rows", v.list.Total())
	}
	v.Update(ui.GroupingMsg(true))
	if v.list.Total() != 2 {
		t.Errorf("grouping on with date sort: want header + PR, got %d rows", v.list.Total())
	}
}

func TestSizeBuckets(t *testing.T) {
	cases := map[int]string{0: "XS", 9: "XS", 10: "S", 49: "S", 50: "M", 249: "M", 250: "L", 999: "L", 1000: "XL"}
	for churn, want := range cases {
		if got := sizeBucket(churn); got != want {
			t.Errorf("sizeBucket(%d) = %q, want %q", churn, got, want)
		}
	}
}
