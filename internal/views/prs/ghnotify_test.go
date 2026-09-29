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

// Reading a notification on github.com (or in any other client) clears the
// mark here too: that is the half that makes the sync bidirectional.
func TestUpstreamReadClearsLocalMark(t *testing.T) {
	v := unreadView(t)
	v.unreadSync = true

	row := pr{Number: 2, URL: "u2"}
	row.Repository.NameWithOwner = "o/r"
	other := pr{Number: 3, URL: "u3"}
	other.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{row, other}}})
	v.unread = map[string]bool{"u2": true, "u3": true}
	v.applySort()

	v.Update(threadsReadMsg{read: map[string]bool{"o/r#2": true}})
	if v.unread["u2"] {
		t.Error("a thread read upstream left its mark here")
	}
	if !v.unread["u3"] {
		t.Error("a thread still unread upstream lost its mark")
	}
}

// With the setting off, upstream state is none of agenda's business.
func TestUpstreamReadIgnoredWhenSyncOff(t *testing.T) {
	v := unreadView(t)
	v.unreadSync = false

	row := pr{Number: 2, URL: "u2"}
	row.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{row}}})
	v.unread = map[string]bool{"u2": true}
	v.applySort()

	v.Update(threadsReadMsg{read: map[string]bool{"o/r#2": true}})
	if !v.unread["u2"] {
		t.Error("upstream state cleared a mark with unread_sync off")
	}
}

// Only read threads clear a mark; the listing includes unread ones too.
func TestOnlyReadThreadsAreCollected(t *testing.T) {
	var read, unread ghThread
	read.Subject.URL = "https://api.github.com/repos/o/r/pulls/1"
	read.Unread = false
	unread.Subject.URL = "https://api.github.com/repos/o/r/pulls/2"
	unread.Unread = true

	got := map[string]bool{}
	for _, t := range []ghThread{read, unread} {
		if k := t.key(); k != "" && !t.Unread {
			got[k] = true
		}
	}
	if !got["o/r#1"] {
		t.Error("a read thread was not collected")
	}
	if got["o/r#2"] {
		t.Error("an unread thread was collected as read")
	}
}
