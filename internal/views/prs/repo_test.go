package prs

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

// A repo's open PRs replace the list until LeaveRepo, which restores the
// configured one and leaves the raw rows untouched.
func TestRepoSetReplacesListTemporarily(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 1, URL: "u1", State: "OPEN"}}}})
	v.Update(repoMsg{from: v, repo: "o/r", page: searchPage{prs: []pr{{Number: 2, URL: "u2", State: "OPEN"}, {Number: 3, URL: "u3", State: "OPEN"}}}})
	if v.list.Any(matchURL("u1")) || !v.list.Any(matchURL("u2")) || !v.list.Any(matchURL("u3")) {
		t.Fatal("the repo set did not replace the list")
	}
	if !v.LeaveRepo() || v.LeaveRepo() {
		t.Error("LeaveRepo should report true once, then false")
	}
	if !v.list.Any(matchURL("u1")) || v.list.Any(matchURL("u2")) {
		t.Error("leaving did not restore the configured list")
	}
}
