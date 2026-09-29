package linear

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func mkIDs(ids ...string) []issue {
	out := make([]issue, len(ids))
	for i, id := range ids {
		out[i].Identifier = id
	}
	return out
}

func TestNewIssues(t *testing.T) {
	fresh := newIssues(mkIDs("SRE-1", "SRE-2"), mkIDs("SRE-2", "SRE-3", "SRE-4"))
	if len(fresh) != 2 || fresh[0].Identifier != "SRE-3" || fresh[1].Identifier != "SRE-4" {
		t.Errorf("newIssues = %v, want SRE-3 and SRE-4", fresh)
	}
	if got := newIssues(nil, mkIDs("SRE-1")); len(got) != 1 {
		t.Errorf("everything is new against an empty prev, got %v", got)
	}
	if got := newIssues(mkIDs("SRE-1"), mkIDs("SRE-1")); got != nil {
		t.Errorf("no change should yield nil, got %v", got)
	}
}

func TestNotifyNewGating(t *testing.T) {
	v := &View{}
	// No notifier: never a command, even with new items.
	if cmd := v.notifyNew(nil, mkIDs("SRE-1")); cmd != nil {
		t.Error("notifyNew without a notifier should be nil")
	}
	v.notifier = fakeNotifier{}
	// Not seeded yet (first fetch): stay quiet.
	if cmd := v.notifyNew(nil, mkIDs("SRE-1")); cmd != nil {
		t.Error("notifyNew before seeding should be nil")
	}
	v.seeded = true
	if cmd := v.notifyNew(mkIDs("SRE-1"), mkIDs("SRE-1", "SRE-2")); cmd == nil {
		t.Error("notifyNew with a genuinely new issue should return a command")
	}
	if cmd := v.notifyNew(mkIDs("SRE-1"), mkIDs("SRE-1")); cmd != nil {
		t.Error("notifyNew with no new issues should be nil")
	}
}

type fakeNotifier struct{}

func (fakeNotifier) Notify(title, body, _ string) tea.Msg { return nil }
