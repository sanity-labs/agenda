package linear

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

var errBoom = errors.New("boom")

func pagedView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	return v
}

func rawIDs(v *View) string {
	var out []string
	for _, i := range v.raw {
		out = append(out, i.Identifier)
	}
	return strings.Join(out, ",")
}

// A page that reports more behind it says so in the status line, reaching
// the end of the list asks for the next page once, and the next page
// appends without repeating an issue that moved between the two fetches.
// The selection stays where it was, and the last page ends the asking.
func TestLaterPagesAppendInOrderWithoutDuplicates(t *testing.T) {
	v := pagedView(t)
	v.Update(loadedMsg{issues: []issue{{Identifier: "A-1"}, {Identifier: "A-2"}}, source: v.defaultSource, cursor: "c1", hasMore: true})
	if s := v.statusText(); !strings.Contains(s, "2 loaded · more below") {
		t.Errorf("status with more pages = %q", s)
	}

	cmd := v.Update(tea.KeyPressMsg{Code: 'G'}) // reaching the end is the signal
	if cmd == nil || !v.page.loading {
		t.Fatalf("reaching the end did not ask for the next page: cmd=%v loading=%v", cmd != nil, v.page.loading)
	}
	if s := v.statusText(); !strings.Contains(s, "fetching more") || !v.Loading() {
		t.Errorf("a page in flight should show: status=%q loading=%v", s, v.Loading())
	}
	// Still at the end while the page is in flight: no second request, or
	// the page would arrive twice.
	if again := v.Update(tea.KeyPressMsg{Code: tea.KeyDown}); again != nil {
		t.Error("a second request was made while the first page was in flight")
	}
	selected := v.list.Selected().Identifier

	v.Update(loadedMsg{issues: []issue{{Identifier: "A-2"}, {Identifier: "A-3"}}, source: v.defaultSource, after: "c1", cursor: "", hasMore: false})
	if got := rawIDs(v); got != "A-1,A-2,A-3" {
		t.Errorf("after the second page raw = %s, want A-1,A-2,A-3 (appended, A-2 once)", got)
	}
	if v.list.Selected().Identifier != selected {
		t.Errorf("the selection moved from %s to %s across a load", selected, v.list.Selected().Identifier)
	}
	if s := v.statusText(); !strings.Contains(s, "3 issues") || strings.Contains(s, "more") {
		t.Errorf("status after the last page = %q", s)
	}
	if v.page.loading || v.fetchMore() != nil {
		t.Error("the last page is in, yet another fetch was offered")
	}
}

// A later page for a source you have since left is dropped, and a failed
// later page keeps the list rather than turning the tab into an error.
func TestStaleOrFailedLaterPagesLeaveTheListAlone(t *testing.T) {
	v := pagedView(t)
	v.Update(loadedMsg{issues: []issue{{Identifier: "A-1"}}, source: v.defaultSource, cursor: "c1", hasMore: true})
	other := navSource{Kind: "all", Label: "All Issues"}
	v.Update(loadedMsg{issues: []issue{{Identifier: "B-1"}}, source: other})
	v.Update(loadedMsg{issues: []issue{{Identifier: "A-9"}}, source: v.defaultSource, after: "c1"})
	if got := rawIDs(v); got != "B-1" {
		t.Errorf("a stale later page was applied: raw = %s", got)
	}

	v.page = pageState{cursor: "c2", hasMore: true}
	v.Update(tea.KeyPressMsg{Code: 'G'})
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(errMsg{err: errBoom})
	if v.err != nil || len(v.raw) != 1 || v.page.loading {
		t.Errorf("a failed later page: err=%v rows=%d loading=%v", v.err, len(v.raw), v.page.loading)
	}
}
