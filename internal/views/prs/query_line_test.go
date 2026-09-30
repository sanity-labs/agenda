package prs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func queryView(t *testing.T, mut func(*config.GitHubConfig)) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.Default().GitHub
	if mut != nil {
		mut(&cfg)
	}
	v := New(cfg, nil, nil, nil)
	v.SetSize(120, 0, 30)
	return v
}

// The effective query has to name what is actually filtering the list, or
// "why is this PR missing" cannot be answered from the screen.
func TestEffectiveQueryNamesTheFiltersInForce(t *testing.T) {
	v := queryView(t, nil)
	q := v.effectiveQuery()
	if !strings.Contains(q, "author:@me") {
		t.Errorf("effective query %q omits the configured filter", q)
	}
	// The review search only runs when that section is shown.
	if strings.Contains(q, "review-requested") {
		t.Errorf("effective query %q names the review search while it is off", q)
	}

	show := true
	v = queryView(t, func(c *config.GitHubConfig) { c.ShowReviewRequested = &show })
	if q := v.effectiveQuery(); !strings.Contains(q, "review-requested") {
		t.Errorf("effective query %q omits the review search while it is on", q)
	}

	// A setting that narrows client-side reads as a term, since that is
	// what it does from the user's side.
	v = queryView(t, func(c *config.GitHubConfig) { c.HideApproved = true })
	if q := v.effectiveQuery(); !strings.Contains(q, "-review:approved") {
		t.Errorf("effective query %q omits hide_approved", q)
	}
}

// The typed filter is not repeated here: it already shows on the filter
// line, and saying it twice is noise.
func TestEffectiveQueryOmitsTheTypedFilter(t *testing.T) {
	v := queryView(t, nil)
	v.list.SetQuery("-label:deps")
	if q := v.effectiveQuery(); strings.Contains(q, "label:deps") {
		t.Errorf("effective query %q repeats the typed filter", q)
	}
}

// The query line is its own row under the status, and the list must be
// sized to leave room or the last row falls off the bottom.
func TestQueryLineGetsItsOwnRow(t *testing.T) {
	v := queryView(t, nil)
	if got := v.headerRows(); got != 2 {
		t.Errorf("headerRows = %d with a query line, want 2", got)
	}

	lines := strings.Split(v.ListView(), "\n")
	if len(lines) < 2 {
		t.Fatalf("ListView has %d lines, want a status and a query line", len(lines))
	}
	if !strings.Contains(ansi.Strip(lines[1]), "author:@me") {
		t.Errorf("line 2 is not the query line: %q", ansi.Strip(lines[1]))
	}

	// No configured filter: no line, and the row is given back.
	v = queryView(t, func(c *config.GitHubConfig) { c.Filter = "" })
	if v.queryLine() != "" {
		t.Errorf("a blank filter still rendered a query line: %q", v.queryLine())
	}
	if got := v.headerRows(); got != 1 {
		t.Errorf("headerRows = %d with no query line, want 1", got)
	}
}

// Both filter styles share one format, so the line reads the same whichever
// is in use.
func TestFilterLinePrefixFollowsTheStyle(t *testing.T) {
	v := queryView(t, nil)
	p := pr{Number: 1, URL: "u", Title: "t", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})

	v.list.SetQuery("persona") // fuzzy
	if got := ansi.Strip(v.list.FilterLine()); !strings.HasPrefix(got, "/") {
		t.Errorf("a fuzzy filter shows %q, want the / prefix", got)
	}

	v.list.SetQuery("-label:deps") // GitHub-style
	got := ansi.Strip(v.list.FilterLine())
	if !strings.HasPrefix(got, ui.Glyph(ui.IconSearch, "?")) {
		t.Errorf("a qualified filter shows %q, want the magnifier prefix", got)
	}
}
