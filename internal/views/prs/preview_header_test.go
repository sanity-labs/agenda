package prs

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
)

// The preview header reads like the list row: metadata over the title, a
// blank line, then one status line that says where the PR is going and
// carries the counts at the right edge.
func TestPreviewHeaderMatchesTheRow(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(60, 80, 24)
	p := pr{Number: 7, URL: "u", Title: "Add the poller", State: "OPEN", HeadRefName: "feat/poller", BaseRefName: "main", Additions: 93}
	p.Repository.NameWithOwner = "o/r"
	p.Author.Login = "me"
	p.Comments.TotalCount = 5
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})

	lines := strings.Split(ansi.Strip(v.PreviewView()), "\n")
	if !strings.HasPrefix(lines[0], "o/r #7") || strings.TrimRight(lines[1], " ") != "Add the poller" || lines[2] != "" {
		t.Errorf("header does not read meta, title, blank:\n%q\n%q\n%q", lines[0], lines[1], lines[2])
	}
	status, health := lines[3], lines[4]
	if !strings.Contains(status, "open  ·  main ← feat/poller") {
		t.Errorf("the status line does not say state and where the PR merges: %q", status)
	}
	if strings.Contains(status, "+93") {
		t.Errorf("the counts belong on the next line, not the status line: %q", status)
	}
	if !strings.HasSuffix(strings.TrimRight(health, " "), "5") || !strings.Contains(health, "+93") {
		t.Errorf("the counts are not on the checks/review line: %q", health)
	}
	if w := lipgloss.Width(health); w != 80 {
		t.Errorf("the counts are not right-aligned to the pane: line is %d wide, pane 80", w)
	}
}
