package prs

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

// 'D' drops drafts from both sections and brings them back; the config key
// sets where it starts, off by default.
func TestHideDraftsTogglesBothSections(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	mine := []pr{{Number: 1, URL: "u1", Title: "ready", State: "OPEN"}, {Number: 2, URL: "u2", Title: "wip", State: "OPEN", IsDraft: true}}
	theirs := []pr{{Number: 3, URL: "u3", Title: "theirs-draft", State: "OPEN", IsDraft: true}}
	v.Update(mineMsg{page: searchPage{prs: mine}})
	v.Update(reviewListMsg{page: searchPage{prs: theirs}})

	count := func() (n int) {
		for _, p := range v.list.Items() {
			if p.Separator == "" {
				n++
			}
		}
		return n
	}
	if count() != 3 {
		t.Fatalf("setup: %d rows, want 3 with drafts shown", count())
	}
	v.Update(press('D'))
	if count() != 1 || v.list.Any(func(p pr) bool { return p.IsDraft }) {
		t.Errorf("'D' did not drop the drafts from both sections: %d rows", count())
	}
	v.Update(press('D'))
	if count() != 3 {
		t.Errorf("'D' again did not bring the drafts back: %d rows", count())
	}

	on := New(config.GitHubConfig{HideDrafts: true}, nil, nil, nil)
	on.Update(mineMsg{page: searchPage{prs: mine}})
	if on.list.Any(func(p pr) bool { return p.IsDraft }) {
		t.Error("hide_drafts: true did not start with drafts hidden")
	}
}
