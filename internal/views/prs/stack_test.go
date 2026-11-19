package prs

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func stacked(base string, parents ...string) pr {
	p := pr{Number: 9, URL: "u", Title: "child", State: "OPEN", HeadRefName: "child", BaseRefName: base}
	p.Repository.NameWithOwner = "o/r"
	p.Repository.DefaultBranchRef.Name = "main"
	for i, s := range parents {
		n := struct {
			Number int `json:"number"`
		}{Number: 4 + i}
		if s == "OPEN" {
			p.BaseRef.Open.Nodes = append(p.BaseRef.Open.Nodes, n)
		} else {
			p.BaseRef.Merged.Nodes = append(p.BaseRef.Merged.Nodes, n)
		}
	}
	return p
}

func TestParentDetection(t *testing.T) {
	fork := stacked("feat", "OPEN")
	fork.IsCrossRepository = true
	cases := []struct {
		name   string
		p      pr
		num    int
		merged bool
	}{
		{"open parent", stacked("feat", "OPEN"), 4, false},
		{"merged parent", stacked("feat", "MERGED"), 4, true},
		{"open wins over an older merged one", stacked("feat", "MERGED", "OPEN"), 5, false},
		{"open wins with several merged present", stacked("feat", "MERGED", "MERGED", "OPEN"), 6, false},
		{"fork is skipped", fork, 0, false},
		{"default branch is not a stack", stacked("main", "MERGED"), 0, false},
		{"default branch is the repo's own, not main", func() pr { p := stacked("trunk", "MERGED"); p.Repository.DefaultBranchRef.Name = "trunk"; return p }(), 0, false},
		{"unrelated base with no PR", stacked("release/1"), 0, false},
	}
	for _, c := range cases {
		if n, m := c.p.parent(); n != c.num || m != c.merged {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, n, m, c.num, c.merged)
		}
	}
}

func TestStackRow(t *testing.T) {
	row := func(p pr) string {
		p.ShowStack = true
		if n, merged := p.parent(); n != 0 && !merged {
			p.StackPos, p.StackTotal = 2, 2
		}
		return ansi.Strip(strings.Split(p.Render(80, false, ui.Highlighter{}), "\n")[0])
	}
	if got := row(stacked("feat", "OPEN")); !strings.Contains(got, "o/r #9 "+ui.IconStack+" 2/2 · child") {
		t.Errorf("open parent not marked: %q", got)
	}
	if got := row(stacked("feat", "MERGED")); !strings.Contains(got, "o/r #9 "+ui.IconStack+" "+ui.IconWarn+" restack · child") {
		t.Errorf("merged parent not flagged: %q", got)
	}
	off := stacked("feat", "MERGED")
	if got := ansi.Strip(strings.Split(off.Render(80, false, ui.Highlighter{}), "\n")[0]); strings.Contains(got, "restack") {
		t.Errorf("marker drawn with show_stacks off: %q", got)
	}
	t.Log("\n" + row(stacked("feat", "OPEN")) + "\n" + row(stacked("feat", "MERGED")))
}

func TestStackPreview(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	status := func(cfg config.GitHubConfig, p pr) string {
		v := New(cfg, nil, nil, nil)
		v.SetSize(60, 100, 24)
		v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
		return strings.Split(ansi.Strip(v.PreviewView()), "\n")[3]
	}
	if got := status(config.GitHubConfig{}, stacked("feat", "OPEN")); !strings.Contains(got, "main ← #4 ← child") || strings.Contains(got, "restack") {
		t.Errorf("open parent chain: %q", got)
	}
	got := status(config.GitHubConfig{}, stacked("feat", "MERGED"))
	if !strings.Contains(got, ui.IconWarn+" base #4 merged, restack") {
		t.Errorf("merged parent not flagged in preview: %q", got)
	}
	t.Log("\n" + got)
	off := false
	if got := status(config.GitHubConfig{ShowStacks: &off}, stacked("feat", "MERGED")); strings.Contains(got, "restack") || strings.Contains(got, "#4") {
		t.Errorf("preview shows stack with show_stacks off: %q", got)
	}
}

func stk(num int, head, base, state string) pr {
	p := pr{Number: num, URL: "u" + strconv.Itoa(num), Title: "t", State: state, HeadRefName: head, BaseRefName: base}
	p.Author.Login = "SurrealTiggi"
	p.Repository.NameWithOwner = "sanity-io/so"
	p.Repository.DefaultBranchRef.Name = "main"
	return p
}

func pair() (parent, child pr) {
	parent = stk(809, "fix/changelog-ssh-remote", "main", "OPEN")
	child = stk(810, "feat/changelog-tag-detect", "fix/changelog-ssh-remote", "OPEN")
	child.BaseRef.Open.Nodes = append(child.BaseRef.Open.Nodes, struct {
		Number int `json:"number"`
	}{809})
	return
}

func rows(v *View) map[int]string {
	out := map[int]string{}
	for _, p := range v.list.Items() {
		if p.Separator == "" {
			out[p.Number] = ansi.Strip(strings.Split(p.Render(80, false, ui.Highlighter{}), "\n")[0])
		}
	}
	return out
}

func TestStackMarkerSurvivesNarrowRows(t *testing.T) {
	parent, child := pair()
	parent.ShowStack, child.ShowStack = true, true
	parent.StackPos, parent.StackTotal = 1, 2
	child.StackPos, child.StackTotal = 2, 2
	for _, w := range []int{60, 80, 100} {
		pr, cr := parent.Render(w, false, ui.Highlighter{}), child.Render(w, false, ui.Highlighter{})
		pl, cl := ansi.Strip(strings.Split(pr, "\n")[0]), ansi.Strip(strings.Split(cr, "\n")[0])
		if !strings.Contains(cl, "#810 "+ui.IconStack+" 2/2") || !strings.Contains(pl, "#809 "+ui.IconStack+" 1/2") {
			t.Errorf("width %d lost a marker:\n%s\n%s", w, pl, cl)
		}
		t.Logf("w=%d\n%s\n%s", w, pl, cl)
	}
}

func TestParentMarkerSurvivesFilters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	parent, child := pair()
	child.IsDraft = true
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(100, 100, 24)
	v.hideDrafts = true
	v.raw = []pr{parent, child}
	v.applySort()
	got := rows(v)
	if _, ok := got[810]; ok {
		t.Fatal("draft child should be hidden")
	}
	if !strings.Contains(got[809], "#809 "+ui.IconStack+" 1/2") {
		t.Errorf("parent not marked with a hidden child: %q", got[809])
	}
	// Both: a middle PR is child and parent, and two children collapse.
	mid := stk(808, "feat/a", "main", "OPEN")
	parent.BaseRefName = "feat/a"
	parent.BaseRef.Open.Nodes = append(parent.BaseRef.Open.Nodes, struct {
		Number int `json:"number"`
	}{808})
	child.IsDraft = false
	other := stk(811, "feat/other", "fix/changelog-ssh-remote", "OPEN")
	v.raw = []pr{mid, parent, child, other}
	v.applySort()
	got = rows(v)
	if !strings.Contains(got[809], "#809 "+ui.IconStack+" 2/3") {
		t.Errorf("middle of a stack: %q", got[809])
	}
	// Off: nothing drawn.
	off := false
	v.cfg.ShowStacks = &off
	v.applySort()
	if got := rows(v); strings.Contains(got[809], ui.IconStack) {
		t.Errorf("markers with show_stacks off: %q", got[809])
	}
}

func TestOpenedRowGetsStackMarks(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	parent, child := pair()
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(100, 100, 24)
	v.raw = []pr{parent}
	v.opened = []pr{child}
	v.applySort()
	if got := rows(v)[810]; !strings.Contains(got, "#810 "+ui.IconStack+" 2/2") {
		t.Errorf("opened row unmarked: %q", got)
	}
}

func TestStackChainWalksDeepStacks(t *testing.T) {
	a := stk(808, "feat/a", "main", "OPEN")
	b := stk(809, "feat/b", "feat/a", "OPEN")
	c := stk(810, "feat/c", "feat/b", "OPEN")
	idx := func(ps ...pr) map[string]pr {
		m := map[string]pr{}
		for _, p := range ps {
			m[p.repo()+"\x00"+p.HeadRefName] = p
		}
		return m
	}
	if got, want := stackChain(c, idx(a, b, c)), "main ← #808 ← #809 ← feat/c"; got != want {
		t.Errorf("deep chain: %q, want %q", got, want)
	}
	if got, want := stackChain(c, idx(b, c)), "… ← feat/a ← #809 ← feat/c"; got != want {
		t.Errorf("missing link: %q, want %q", got, want)
	}
	loop := stk(808, "feat/a", "feat/c", "OPEN")
	if got, want := stackChain(c, idx(loop, b, c)), "… ← feat/c ← #808 ← #809 ← feat/c"; got != want {
		t.Errorf("cycle: %q, want %q", got, want)
	}
	child := stk(9, "child", "feat", "OPEN")
	child.BaseRef.Open.Nodes = append(child.BaseRef.Open.Nodes, struct {
		Number int `json:"number"`
	}{4})
	if got, want := stackChain(child, idx(child)), "main ← #4 ← child"; got != want {
		t.Errorf("parent only known by baseRef: %q, want %q", got, want)
	}
}

func TestPreviewShowsDeepChain(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := stk(808, "feat/a", "main", "OPEN")
	b := stk(809, "feat/b", "feat/a", "OPEN")
	c := stk(810, "feat/c", "feat/b", "OPEN")
	c.BaseRef.Open.Nodes = append(c.BaseRef.Open.Nodes, struct {
		Number int `json:"number"`
	}{809})
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(60, 100, 24)
	v.raw = []pr{a, b, c}
	v.applySort()
	v.list.Select(func(p pr) bool { return p.Number == 810 })
	if got := ansi.Strip(v.PreviewView()); !strings.Contains(got, "main ← #808 ← #809 ← feat/c  "+ui.IconStack+" 3/3") {
		t.Errorf("preview chain: %q", got)
	}
}
