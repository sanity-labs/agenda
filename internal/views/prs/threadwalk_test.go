package prs

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func walkView(t *testing.T, threads ...prThread) *View {
	t.Helper()
	v := &View{listW: 60, prevW: 40, height: 40}
	v.list.SetRowHeight(2)
	v.raw = []pr{mkPR("u1", "theirs", 0)}
	v.applySort()
	st := &commentsState{done: true}
	st.data.ReviewThreads.Nodes = threads
	v.comments = map[string]*commentsState{"u1": st}
	return v
}

func hasCmd(v *View, id string) bool {
	for _, c := range v.Commands() {
		if c.ID == id {
			return true
		}
	}
	return false
}

func fakeResolve(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := resolveThread
	resolveThread = func(id string) error { got = append(got, id); return nil }
	t.Cleanup(func() { resolveThread = old })
	return &got
}

func runCmd(v *View, cmd tea.Cmd) {
	if cmd != nil {
		v.Update(cmd())
	}
}

func TestResolveCommandOnlyWithUnresolved(t *testing.T) {
	v := walkView(t, mkThread("a", "x.go", intp(1), true, false, "done"))
	if hasCmd(v, "resolve-threads") {
		t.Error("offered with every thread resolved")
	}
	v.comments["u1"].data.ReviewThreads.Nodes[0].IsResolved = false
	if !hasCmd(v, "resolve-threads") {
		t.Error("not offered with an unresolved thread")
	}
	delete(v.comments, "u1")
	if !hasCmd(v, "resolve-threads") {
		t.Error("not offered while threads are unknown")
	}
}

func TestWalkOrderAndBounds(t *testing.T) {
	v := walkView(t,
		mkThread("b2", "b.go", intp(9), false, false, "b9"),
		mkThread("done", "a.go", intp(1), true, false, "x"),
		mkThread("a2", "a.go", intp(20), false, false, "a20"),
		mkThread("a1", "a.go", intp(3), false, false, "a3"),
		mkThread("old", "a.go", nil, false, true, "outdated"))
	v.openWalk(v.list.Selected())
	var ids []string
	for _, th := range v.walk.threads {
		ids = append(ids, th.ID)
	}
	if got := strings.Join(ids, ","); got != "a1,a2,old,b2" {
		t.Fatalf("order = %s", got)
	}
	v.Update(press('p'))
	if v.walk.idx != 0 {
		t.Error("previous went below 0")
	}
	for i := 0; i < 6; i++ {
		v.Update(press('n'))
	}
	if v.walk.idx != 3 {
		t.Errorf("next did not stop at the last thread: %d", v.walk.idx)
	}
	v.Update(press('p'))
	if v.walk.idx != 2 || !strings.Contains(ansi.Strip(v.walkOverlay()), "3/4 a.go (outdated)") {
		t.Errorf("idx=%d\n%s", v.walk.idx, v.walkOverlay())
	}
}

func TestWalkResolveAdvancesThenFinishes(t *testing.T) {
	got := fakeResolve(t)
	v := walkView(t,
		mkThread("t1", "a.go", intp(1), false, false, "one"),
		mkThread("t2", "a.go", intp(2), false, false, "two"))
	v.openWalk(v.list.Selected())
	runCmd(v, v.Update(press('r')))
	if len(*got) != 1 || (*got)[0] != "t1" {
		t.Fatalf("resolved %v, want [t1]", *got)
	}
	if v.walk.threads[v.walk.idx].ID != "t2" {
		t.Error("did not advance to t2")
	}
	if !v.comments["u1"].data.ReviewThreads.Nodes[0].IsResolved {
		t.Error("cache still shows t1 open")
	}
	runCmd(v, v.Update(press('r')))
	if len(*got) != 2 || (*got)[1] != "t2" {
		t.Fatalf("resolved %v", *got)
	}
	if !strings.Contains(v.walkOverlay(), "All threads resolved") {
		t.Error("no empty state")
	}
	v.Update(press('x'))
	if v.walk != nil {
		t.Error("any key should close the empty state")
	}
	if hasCmd(v, "resolve-threads") {
		t.Error("command still offered with nothing left")
	}
}

func TestWalkResolveFailureAndEsc(t *testing.T) {
	old := resolveThread
	resolveThread = func(string) error { return errors.New("nope") }
	t.Cleanup(func() { resolveThread = old })
	v := walkView(t, mkThread("t1", "a.go", intp(1), false, false, "one"))
	v.openWalk(v.list.Selected())
	runCmd(v, v.Update(press('r')))
	if len(v.walk.threads) != 1 || !strings.Contains(v.walkOverlay(), "resolve failed: nope") {
		t.Error("failure should keep the thread and say why")
	}
	v.Update(special(tea.KeyEsc))
	if v.walk != nil {
		t.Error("esc should close")
	}
	v.openWalk(v.list.Selected())
	v.Update(special(tea.KeyEsc))
	if v.walk != nil {
		t.Error("esc should close the walk")
	}
}

func TestWalkRender(t *testing.T) {
	v := walkView(t, mkThread("t1", "internal/some/long/path/file.go", intp(12), false, true,
		"This is a long review comment that has to be wrapped to the popup width without growing it.", "reply"))
	for _, w := range []int{40, 120} {
		v.listW, v.prevW = w, 0
		v.openWalk(v.list.Selected())
		out := v.walkOverlay()
		t.Logf("width %d\n%s", w, out)
		for _, l := range strings.Split(out, "\n") {
			if lipgloss.Width(l) > w+2 && w < 100 {
				t.Errorf("line wider than the screen: %q", ansi.Strip(l))
			}
		}
	}
}

func TestWalkResolvesTheShownThread(t *testing.T) {
	got := fakeResolve(t)
	v := walkView(t,
		mkThread("t1", "a.go", intp(1), false, false, "one"),
		mkThread("t2", "a.go", intp(2), false, false, "two"),
		mkThread("t3", "a.go", intp(3), false, false, "three"))
	v.openWalk(v.list.Selected())
	v.Update(press('n'))
	runCmd(v, v.Update(press('r')))
	if len(*got) != 1 || (*got)[0] != "t2" || v.walk.threads[v.walk.idx].ID != "t3" {
		t.Errorf("resolved %v, now on %s", *got, v.walk.threads[v.walk.idx].ID)
	}
}

// DESIGN.md reserves ← for closing a float, so it must not step back.
func TestWalkLeftClosesPopup(t *testing.T) {
	v := walkView(t, mkThread("a", "a.go", intp(1), false, false, "x"))
	v.openWalk(v.list.Selected())
	v.Update(special(tea.KeyLeft))
	if v.walk != nil {
		t.Error("← left the thread popup open")
	}
}
