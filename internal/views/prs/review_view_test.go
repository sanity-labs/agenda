package prs

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

// 'r' opens the change beside the popup: the file list by default, the
// unified diff when review_view says so, nothing without diff_pane.
func TestReviewOpensTheConfiguredView(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  config.GitHubConfig
		want paneMode
	}{
		{"default", config.GitHubConfig{DiffPane: true}, paneFiles},
		{"unified", config.GitHubConfig{DiffPane: true, ReviewView: "unified"}, paneDiff},
		{"unknown value", config.GitHubConfig{DiffPane: true, ReviewView: "split"}, paneFiles},
		{"no diff pane", config.GitHubConfig{ReviewView: "unified"}, paneBody},
	} {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		v := New(c.cfg, nil, nil, nil)
		v.SetSize(90, 40, 20)
		p := pr{Number: 1, URL: "u", Title: "t", State: "OPEN"}
		p.Repository.NameWithOwner = "o/r"
		v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
		v.Update(press('r'))
		if v.review == nil {
			t.Fatalf("%s: 'r' did not open the review popup", c.name)
		}
		if v.pane != c.want {
			t.Errorf("%s: 'r' opened pane %v, want %v", c.name, v.pane, c.want)
		}
	}
}
