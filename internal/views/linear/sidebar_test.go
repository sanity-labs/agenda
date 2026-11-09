package linear

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func sideIssue(milestone string, labels int, desc string) issue {
	i := withPR(issue{Identifier: "SRE-1", Title: "Title", Description: desc, PriorityLabel: "High", Priority: 2},
		"https://github.com/o/r/pull/5", "Fix the thing")
	i.State.Name, i.State.Type = "In Progress", "started"
	i.Project.Name = "Big Project"
	i.ProjectMilestone.Name = milestone
	for n := 0; n < labels; n++ {
		i.Labels.Nodes = append(i.Labels.Nodes, label{Name: "label" + string(rune('a'+n))})
	}
	parent := issueRef{Identifier: "SRE-100", Title: "Epic", URL: "u"}
	i.Parent = &parent
	return i
}

func sideView(t *testing.T, prevW int, on bool, i issue) []string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t", DetailSidebar: &on}, nil, nil, nil)
	v.SetSize(90, prevW, 60)
	v.Update(loadedMsg{issues: []issue{i}, source: v.defaultSource})
	v.Update(ui.PreviewShownMsg(true))
	return strings.Split(ansi.Strip(v.PreviewView()), "\n")
}

func lineOf(lines []string, s string) int {
	for n, l := range lines {
		if strings.Contains(l, s) {
			return n
		}
	}
	return -1
}

func TestSidebarOrderAndColumn(t *testing.T) {
	lines := sideView(t, 160, true, sideIssue("M1", 2, "body words"))
	prev := -1
	for _, s := range []string{"In Progress", "Big Project", "M1", "Labels", "labela", "labelb", "Pull requests", "o/r#5", "Related issues", "SRE-100"} {
		n := lineOf(lines, s)
		if n <= prev {
			t.Fatalf("%q at line %d, want after %d:\n%s", s, n, prev, strings.Join(lines, "\n"))
		}
		prev = n
	}
	if c := strings.Index(lines[lineOf(lines, "Big Project")], "Big Project"); c < 160*3/4-2 {
		t.Errorf("sidebar starts at col %d, want the right quarter", c)
	}
	if strings.Contains(lines[lineOf(lines, "Description")], "Labels") {
		t.Error("Description and the sidebar share a heading line")
	}
}

func TestSidebarMilestoneOnlyWhenSet(t *testing.T) {
	with := strings.Join(sideView(t, 160, true, sideIssue("M1", 0, "x")), "\n")
	without := strings.Join(sideView(t, 160, true, sideIssue("", 0, "x")), "\n")
	if !strings.Contains(with, "M1") || strings.Contains(without, ui.IconMilestone) {
		t.Errorf("milestone shown wrongly:\nwith:\n%s\nwithout:\n%s", with, without)
	}
}

func TestSidebarMilestoneIsAChildOfTheProject(t *testing.T) {
	lines := sideView(t, 160, true, sideIssue("M1", 0, "x"))
	p := lineOf(lines, "Big Project")
	if p < 0 || p+1 >= len(lines) {
		t.Fatalf("no project line:\n%s", strings.Join(lines, "\n"))
	}
	colOf := func(l, s string) int { return ansi.StringWidth(l[:max(strings.Index(l, s), 0)]) }
	col := colOf(lines[p], "◇")
	if got := lines[p+1]; !strings.Contains(got, "  └ ") || colOf(got, "  └ ") != col || !strings.HasSuffix(strings.TrimSpace(got), "M1") {
		t.Errorf("milestone line %q, want └ M1 under the project at col %d:\n%s", got, col, strings.Join(lines, "\n"))
	}
}

func TestSidebarMilestoneConnectorDimNameNot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	on := true
	v := New(config.LinearConfig{Token: "t", DetailSidebar: &on}, nil, nil, nil)
	if got := v.sidebar(sideIssue("M1", 0, "x"), 40); !strings.Contains(got, ui.Dim.Render("└")+" "+ui.Glyph(ui.IconMilestone, ">")+"M1") {
		t.Errorf("want a dim connector and a plain name, got:\n%q", got)
	}
}

func TestSidebarPadsShorterColumn(t *testing.T) {
	long := strings.Repeat("word ", 400)
	for name, c := range map[string]struct {
		desc   string
		labels int
		last   string
	}{
		"tall sidebar":     {"short", 8, "l to jump to one"},
		"tall description": {long, 0, "word"},
	} {
		lines := sideView(t, 160, true, sideIssue("", c.labels, c.desc))
		if p := lineOf(lines, "Big Project"); p < 0 || strings.Index(lines[p], "Big Project") < 100 {
			t.Fatalf("%s: no sidebar column:\n%s", name, strings.Join(lines, "\n"))
		}
		cm := lineOf(lines, "Comments")
		last := -1
		for n, l := range lines[:max(cm, 0)] {
			if strings.Contains(l, c.last) {
				last = n
			}
		}
		if cm < 0 || last < 0 || cm <= last+1 {
			t.Errorf("%s: comments at %d, last column line at %d:\n%s", name, cm, last, strings.Join(lines, "\n"))
		}
		if strings.Count(strings.Join(lines, "\n"), "Comments") != 1 {
			t.Errorf("%s: comments header repeated", name)
		}
	}
}

func TestSidebarFallsBackWhenNarrowOrOff(t *testing.T) {
	for name, c := range map[string]struct {
		w  int
		on bool
	}{"narrow": {90, true}, "off": {160, false}} {
		lines := sideView(t, c.w, c.on, sideIssue("M1", 1, "body words"))
		pr := lineOf(lines, "Pull requests")
		if pr < 0 || strings.Contains(lines[pr], "body words") || lineOf(lines, "Description") > lineOf(lines, "body words") {
			t.Errorf("%s: not the stacked layout:\n%s", name, strings.Join(lines, "\n"))
		}
		if strings.Contains(strings.Join(lines, "\n"), "Labels") {
			t.Errorf("%s: sidebar content leaked into the stacked layout", name)
		}
	}
}

func TestSidebarTruncatesLongLines(t *testing.T) {
	i := sideIssue("M1", 0, "x")
	i.Project.Name = strings.Repeat("Long", 30)
	lines := sideView(t, 160, true, i)
	if p := lineOf(lines, "LongLong"); p < 0 || !strings.HasSuffix(strings.TrimSpace(lines[p]), "…") {
		t.Fatalf("project not truncated with an ellipsis:\n%s", strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 160 {
			t.Fatalf("line wider than the pane (%d): %q", ansi.StringWidth(l), l)
		}
	}
}
