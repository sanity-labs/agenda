package prs

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The notification subject is an API URL while rows carry the HTML one, so
// matching goes through repo and number. Getting this wrong marks the wrong
// PR read in someone's real inbox.
func TestThreadKey(t *testing.T) {
	cases := []struct {
		name, url, want string
	}{
		{"pull request", "https://api.github.com/repos/sanity-io/terraform/pulls/2235", "sanity-io/terraform#2235"},
		{"issue", "https://api.github.com/repos/sanity-labs/agenda/issues/7", "sanity-labs/agenda#7"},
		{"trailing slash", "https://api.github.com/repos/o/r/pulls/1/", "o/r#1"},
		{"a release is not a PR", "https://api.github.com/repos/o/r/releases/9", ""},
		{"a discussion is not a PR", "https://api.github.com/repos/o/r/discussions/3", ""},
		{"empty subject", "", ""},
		{"too short", "https://api.github.com/pulls/1", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var th ghThread
			th.Subject.URL = c.url
			if got := th.key(); got != c.want {
				t.Errorf("key(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

// A row only queues a write-back when the setting is on. Checked on
// clearUnread directly: the keypress path drains the queue in the same
// call, so watching syncPending after an Update always reads zero.
func TestSyncOnlyQueuesWhenEnabled(t *testing.T) {
	for _, on := range []bool{false, true} {
		v := unreadView(t)
		v.unreadSync = on
		v.Update(ui.PreviewShownMsg(true))

		row := pr{Number: 2, URL: "u2"}
		row.Repository.NameWithOwner = "o/r"
		v.Update(mineMsg{page: searchPage{prs: []pr{row}}})
		v.unread = map[string]bool{"u2": true}
		v.applySort()

		v.clearUnread()
		if got := len(v.syncPending) > 0; got != on {
			t.Errorf("unreadSync=%v queued a write-back: %v, want %v", on, got, on)
		}
		if v.unread["u2"] {
			t.Error("the mark was not cleared regardless of the sync setting")
		}
	}
}

// A row with no repo (a separator, or a partially-decoded row) must never
// produce a write-back: there is nothing to address it to.
func TestSyncSkipsRowsWithNoRepo(t *testing.T) {
	v := unreadView(t)
	v.unreadSync = true
	v.Update(ui.PreviewShownMsg(true))
	v.Update(mineMsg{page: searchPage{prs: []pr{{Number: 2, URL: "u2"}}}})
	v.unread = map[string]bool{"u2": true}
	v.applySort()

	v.clearUnread()
	if len(v.syncPending) != 0 {
		t.Errorf("queued a write-back for a row with no repo: %d", len(v.syncPending))
	}
}
