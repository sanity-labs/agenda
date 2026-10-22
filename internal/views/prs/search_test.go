package prs

import (
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/config"
)

func TestScopeRefsMatchEveryWord(t *testing.T) {
	repos := []string{"sanity-io/argocd-apps", "sanity-io/terraform", "sanity-labs/agenda"}
	var got []string
	for _, r := range scopeRefs("io argo", repos) {
		got = append(got, r.ID)
	}
	if len(got) != 1 || got[0] != "sanity-io/argocd-apps" {
		t.Errorf("io argo matched %v", got)
	}
	if refs := scopeRefs("other/repo", repos); len(refs) != 1 || refs[0].ID != "other/repo" {
		t.Errorf("an unknown owner/name should be offered as typed, got %+v", refs)
	}
}

// A PR neither search lists lands selected in an OPENED section, even with
// hide_approved on, so a jump to it opens it here.
func TestFetchedPRLandsSelected(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{HideApproved: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	a := pr{Number: 1, URL: "u1", Title: "a", State: "OPEN"}
	a.Repository.NameWithOwner = "o/r"
	v.Update(reviewListMsg{page: searchPage{prs: []pr{a}}})

	b := pr{Number: 9, URL: "u9", Title: "b", State: "OPEN", ReviewDecision: "APPROVED"}
	b.Repository.NameWithOwner = "x/y"
	v.Update(prFetchedMsg{from: v, url: "u9", pr: b})
	if v.list.Selected().URL != "u9" {
		t.Fatalf("selected %q, want the fetched PR", v.list.Selected().URL)
	}
	if !v.HasRef("u9") {
		t.Error("the fetched PR is not in the list")
	}
}

// A number also finds loaded PRs it prefixes; the exact hit leads whatever
// the sort, and the list stops at the limit.
func TestNumberPrefixAndOrder(t *testing.T) {
	old := pr{Number: 85, URL: "u85", UpdatedAt: time.Unix(1, 0)}
	a := pr{Number: 8500, URL: "u8500", UpdatedAt: time.Unix(2, 0)}
	b := pr{Number: 8513, URL: "u8513", UpdatedAt: time.Unix(3, 0)}
	other := pr{Number: 123, URL: "u123"}
	nodes, err := numberMatches("85", nil, "", []pr{a, other, b}, 5)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("prefix matches = %+v, %v; want 8500 and 8513", nodes, err)
	}
	nodes = append(nodes, searchNode{Typename: "PullRequest", pr: old})
	var got []int
	for _, n := range sortNodes(nodes, 85, sortRecent, false, 2) {
		got = append(got, n.Number)
	}
	if len(got) != 2 || got[0] != 85 || got[1] != 8513 {
		t.Errorf("order = %v, want [85 8513]", got)
	}
}
