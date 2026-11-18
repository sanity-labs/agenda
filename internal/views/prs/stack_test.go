package prs

import (
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
		p.BaseRef.AssociatedPullRequests.Nodes = append(p.BaseRef.AssociatedPullRequests.Nodes, struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		}{Number: 4 + i, State: s})
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
		return ansi.Strip(strings.Split(p.Render(80, false, ui.Highlighter{}), "\n")[0])
	}
	if got := row(stacked("feat", "OPEN")); !strings.Contains(got, "· child · ↳ #4") {
		t.Errorf("open parent not marked: %q", got)
	}
	if got := row(stacked("feat", "MERGED")); !strings.Contains(got, "· child · "+ui.IconWarn+" restack") {
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
