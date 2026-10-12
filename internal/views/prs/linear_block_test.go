package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/store"
	"github.com/sanity-labs/agenda/internal/ui"
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

	// With the issue known to the store, the row carries its status and title.
	s := store.New()
	s.PutIssues([]store.Issue{{Identifier: "SRE-12", Title: "Poll the thing", State: "In Progress", StateType: "started"}})
	v.store = s
	v.bodyKey = ""
	text = ansi.Strip(v.PreviewView())
	if !strings.Contains(text, ui.IconIssueInProgress+" SRE-12  Poll the thing") {
		t.Errorf("row is not glyph, id, title:\n%s", text)
	}

	plain := pr{Number: 2, URL: "u2", Title: "Nothing to see", State: "OPEN"}
	plain.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{plain}}})
	if text := ansi.Strip(v.PreviewView()); strings.Contains(text, "jump to ticket") {
		t.Errorf("a PR with no Linear reference shows the block:\n%s", text)
	}
}

// Selecting a PR that names a ticket the store does not know asks the
// Linear view for it, once; a known ticket is not asked for.
func TestUnknownLinearTicketIsAskedForOnce(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := store.New()
	s.PutIssues([]store.Issue{{Identifier: "SRE-1", Title: "known", StateType: "started"}})
	v := New(config.GitHubConfig{}, nil, nil, s)
	v.SetSize(90, 60, 24)
	a := pr{Number: 1, URL: "u1", Title: "Known (SRE-1)", State: "OPEN"}
	b := pr{Number: 2, URL: "u2", Title: "Parked (SRE-77)", State: "OPEN"}
	for _, p := range []*pr{&a, &b} {
		p.Repository.NameWithOwner = "o/r"
	}
	cmd := v.Update(mineMsg{page: searchPage{prs: []pr{a, b}}})
	if asked := resolveIDs(cmd); len(asked) != 0 {
		t.Errorf("a known ticket was asked for: %v", asked)
	}
	cmd = v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if asked := resolveIDs(cmd); len(asked) != 1 || asked[0] != "SRE-77" {
		t.Fatalf("asked = %v, want SRE-77", asked)
	}
	v.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if asked := resolveIDs(v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})); len(asked) != 0 {
		t.Errorf("the ticket was asked for again: %v", asked)
	}
}

// resolveIDs runs a batch and collects the ids of any resolve request in it.
func resolveIDs(cmd tea.Cmd) []string {
	var ids []string
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case ui.ResolveRefsMsg:
			ids = append(ids, m.IDs...)
		case tea.BatchMsg:
			for _, sub := range m {
				walk(sub)
			}
		}
	}
	walk(cmd)
	return ids
}

// Words that look like tickets (WAVE-2, SHARD-02 in a Terraform PR) are
// not shown once Linear has shown which teams exist; a real id from a
// known team is, even before its lookup lands.
func TestLinearBlockShowsOnlyVouchedIds(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := store.New()
	s.PutIssues([]store.Issue{{Identifier: "SRE-1", Title: "known"}})
	v := New(config.GitHubConfig{}, nil, nil, s)
	v.SetSize(90, 60, 24)
	p := pr{Number: 1, URL: "u", Title: "Rotate WAVE-2 and SHARD-02 (SRE-12)", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	text := ansi.Strip(v.PreviewView())
	if !strings.Contains(text, "SRE-12") {
		t.Errorf("a known-team id is missing:\n%s", text)
	}
	if strings.Contains(text, "WAVE-2") || strings.Contains(text, "SHARD-02") {
		t.Errorf("words that only look like tickets are shown:\n%s", text)
	}
	if got := len(v.Refs()); got != 1 {
		t.Errorf("picker has %d Linear refs, want 1", got)
	}
	s.MarkMissing("SRE-12")
	v.bodyKey = ""
	if text := ansi.Strip(v.PreviewView()); strings.Contains(text, "SRE-12") {
		t.Errorf("an id a lookup refused is still shown:\n%s", text)
	}
}
