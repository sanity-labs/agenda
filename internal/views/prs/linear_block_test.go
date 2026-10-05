package prs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
)

// The summary names the Linear issue a PR references and says how to get
// there; a PR without one shows no such block.
func TestSummaryNamesTheLinearIssue(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(90, 60, 24)
	p := pr{Number: 1, URL: "u", Title: "Add the poller (SRE-12)", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	text := ansi.Strip(v.PreviewView())
	if !strings.Contains(text, "Linear") || !strings.Contains(text, "SRE-12") || !strings.Contains(text, "l to jump to ticket") {
		t.Errorf("summary lacks the Linear block:\n%s", text)
	}

	plain := pr{Number: 2, URL: "u2", Title: "Nothing to see", State: "OPEN"}
	plain.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{plain}}})
	if text := ansi.Strip(v.PreviewView()); strings.Contains(text, "jump to ticket") {
		t.Errorf("a PR with no Linear reference shows the block:\n%s", text)
	}
}
