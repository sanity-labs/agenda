package prs

import (
	"testing"
	"time"
)

// mk builds a minimal pr for ordering tests. ci and review are the raw GitHub
// enum values ("" for none); ageMinutes is how long ago it was updated.
func mk(repo string, num int, ci, review string, churn, ageMinutes int) pr {
	var p pr
	p.Number = num
	p.Repository.NameWithOwner = repo
	p.ReviewDecision = review
	p.Additions, p.Deletions = churn, 0
	p.UpdatedAt = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).
		Add(-time.Duration(ageMinutes) * time.Minute)
	if ci != "" {
		p.Commits.Nodes = append(p.Commits.Nodes, commitNode{})
		p.Commits.Nodes[0].Commit.StatusCheckRollup.State = ci
	}
	return p
}

func nums(in []pr) []int {
	out := make([]int, len(in))
	for i, p := range in {
		out[i] = p.Number
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortPRsByDate(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "", "", 0, 50),
		mk("o/a", 2, "", "", 0, 5),
		mk("o/a", 3, "", "", 0, 20),
	}
	want := []int{2, 3, 1} // newest first
	if got := nums(sortPRs(in, sortRecent, false)); !equal(got, want) {
		t.Errorf("sortRecent = %v, want %v", got, want)
	}
}

func TestSortPRsByReview(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "", "APPROVED", 0, 1),
		mk("o/a", 2, "", "", 0, 1),
		mk("o/a", 3, "", "CHANGES_REQUESTED", 0, 1),
		mk("o/a", 4, "", "REVIEW_REQUIRED", 0, 1),
	}
	// changes requested → review required → no decision → approved
	want := []int{3, 4, 2, 1}
	if got := nums(sortPRs(in, sortReview, false)); !equal(got, want) {
		t.Errorf("sortReview = %v, want %v", got, want)
	}
}

func TestSortPRsByChecks(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "SUCCESS", "", 0, 1),
		mk("o/a", 2, "", "", 0, 1), // no checks
		mk("o/a", 3, "FAILURE", "", 0, 1),
		mk("o/a", 4, "PENDING", "", 0, 1),
		mk("o/a", 5, "ERROR", "", 0, 1),
	}
	// failing (both FAILURE and ERROR) → running → no checks → passing
	want := []int{3, 5, 4, 2, 1}
	if got := nums(sortPRs(in, sortChecks, false)); !equal(got, want) {
		t.Errorf("sortChecks = %v, want %v", got, want)
	}
}

func TestSortPRsByReviewTiebreaksOnRecency(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "", "APPROVED", 0, 99),
		mk("o/a", 2, "", "APPROVED", 0, 1),
	}
	want := []int{2, 1}
	if got := nums(sortPRs(in, sortReview, false)); !equal(got, want) {
		t.Errorf("sortReview tiebreak = %v, want %v", got, want)
	}
}

func TestSortPRsByRepo(t *testing.T) {
	in := []pr{
		mk("org/Zeta", 1, "", "", 0, 1),
		mk("org/alpha", 2, "", "", 0, 99),
		mk("org/alpha", 3, "", "", 0, 1),
	}
	// repos case-insensitively A→Z, newest first within a repo
	want := []int{3, 2, 1}
	if got := nums(sortPRs(in, sortRepo, false)); !equal(got, want) {
		t.Errorf("sortRepo = %v, want %v", got, want)
	}
}

func TestSortPRsBySize(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "", "", 500, 1),
		mk("o/a", 2, "", "", 3, 1),
		mk("o/a", 3, "", "", 40, 1),
	}
	want := []int{2, 3, 1} // smallest diff first
	if got := nums(sortPRs(in, sortSize, false)); !equal(got, want) {
		t.Errorf("sortSize = %v, want %v", got, want)
	}
}

func TestSortPRsDoesNotMutateInput(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "SUCCESS", "APPROVED", 900, 1),
		mk("o/a", 2, "FAILURE", "", 1, 99),
	}
	before := nums(in)
	sortPRs(in, sortChecks, false)
	if got := nums(in); !equal(got, before) {
		t.Errorf("input reordered: %v, want %v", got, before)
	}
}

func TestSortOrderNamesEveryMode(t *testing.T) {
	for _, m := range sortOrder {
		if sortName[m] == "" {
			t.Errorf("sort mode %d has no name", m)
		}
	}
}

func TestSortPRsReversed(t *testing.T) {
	in := []pr{
		mk("o/a", 1, "SUCCESS", "APPROVED", 500, 50),
		mk("o/a", 2, "FAILURE", "CHANGES_REQUESTED", 3, 5),
		mk("o/a", 3, "PENDING", "REVIEW_REQUIRED", 40, 20),
	}
	cases := map[sortMode][]int{
		sortRecent: {1, 3, 2}, // oldest first
		sortReview: {1, 3, 2}, // approved first
		sortChecks: {1, 3, 2}, // passing first
		sortSize:   {1, 3, 2}, // biggest diff first
	}
	for mode, want := range cases {
		if got := nums(sortPRs(in, mode, true)); !equal(got, want) {
			t.Errorf("reversed %s = %v, want %v", sortName[mode], got, want)
		}
	}
}

// Reversing negates the comparison, so for inputs with no fully-tied items the
// reversed list is the forward list backwards, in every mode.
func TestSortPRsReversedIsExactInverse(t *testing.T) {
	in := []pr{
		mk("o/b", 1, "SUCCESS", "APPROVED", 500, 50),
		mk("o/a", 2, "FAILURE", "CHANGES_REQUESTED", 3, 5),
		mk("o/c", 3, "PENDING", "REVIEW_REQUIRED", 40, 20),
		mk("o/a", 4, "", "", 40, 7),
	}
	for _, mode := range sortOrder {
		fwd := nums(sortPRs(in, mode, false))
		rev := nums(sortPRs(in, mode, true))
		want := make([]int, len(fwd))
		for i, n := range fwd {
			want[len(fwd)-1-i] = n
		}
		if !equal(rev, want) {
			t.Errorf("%s: reversed = %v, want %v (inverse of %v)",
				sortName[mode], rev, want, fwd)
		}
	}
}
