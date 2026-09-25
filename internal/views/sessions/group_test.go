package sessions

import (
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/ui"
)

func mkSess(cwd, tl string, msgs int) session {
	s := session{Tool: tool(tl), Path: cwd + "/x.jsonl", Updated: time.Now()}
	s.Cwd = cwd
	s.Msgs = msgs
	return s
}

func TestSessionGroupLabels(t *testing.T) {
	if groupLabelFn(sortMsgs) != nil {
		t.Error("msgs sort has no sensible buckets and must stay flat")
	}
	if got := groupLabelFn(sortTool)(mkSess("/tmp", "codex", 1)); got != "codex" {
		t.Errorf("tool label = %q", got)
	}
	if groupLabelFn(sortCwd) == nil || groupLabelFn(sortRecent) == nil {
		t.Error("cwd and recent sorts should declare grouping dimensions")
	}
}

func TestSessionGroupingByTool(t *testing.T) {
	v := &View{}
	v.list.SetRowHeight(2)
	v.raw = []session{mkSess("/a", "codex", 1), mkSess("/b", "claude", 2), mkSess("/c", "claude", 3)}
	v.sort = sortTool
	v.Update(ui.GroupingMsg(true))

	var got []string
	for _, s := range v.list.Items() {
		if s.Separator != "" {
			got = append(got, "lane:"+s.Separator)
		} else {
			got = append(got, s.Cwd)
		}
	}
	// Tool sort is alphabetical: claude lane (two sessions), then codex.
	want := []string{"lane:claude", "/c", "/b", "lane:codex", "/a"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestWheelDisarmsPendingDelete(t *testing.T) {
	v := &View{}
	v.list.SetRowHeight(2)
	v.list.SetSize(40, 20)
	v.raw = []session{mkSess("/a", "claude", 1), mkSess("/b", "claude", 2)}
	v.applyView()
	v.confirmDel = true

	v.ScrollList(1)
	if v.confirmDel {
		t.Error("delete still armed after the wheel moved the selection; y would delete the new row")
	}
}
