package prs

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// The bar spans the frame and never overflows it, at any width: Width() is
// the outer box, so sizing it to the full width pushes the border past the
// screen.
func TestQueryBarFitsTheWidth(t *testing.T) {
	for _, w := range []int{200, 120, 100, 60, 40, 31, 30, 29, 10} {
		v := queryView(t, nil)
		bar := v.QueryBar(w)
		if bar == "" {
			continue // too narrow for a box, which is allowed
		}
		for i, line := range strings.Split(bar, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("width %d: bar line %d is %d wide", w, i, got)
			}
		}
		// Three rows: two borders and the query. A wrapped query would be
		// more, and would silently eat a list row.
		if h := lipgloss.Height(bar); h != 3 {
			t.Errorf("width %d: bar is %d rows, want 3 (a wrapped query)", w, h)
		}
	}
}

// A query longer than the bar is truncated, not wrapped.
func TestLongQueryIsTruncated(t *testing.T) {
	v := queryView(t, func(c *config.GitHubConfig) {
		c.Filter = strings.Repeat("author:someone-with-a-long-name ", 12)
	})
	bar := v.QueryBar(80)
	if h := lipgloss.Height(bar); h != 3 {
		t.Fatalf("a long query made the bar %d rows, want 3", h)
	}
	if !strings.Contains(ansi.Strip(bar), "…") {
		t.Error("a truncated query has no ellipsis to say so")
	}
}

// Below a usable width the bar is dropped rather than rendering a border
// with no room for the query inside it.
func TestNoBarWhenTooNarrow(t *testing.T) {
	v := queryView(t, nil)
	if bar := v.QueryBar(20); bar != "" {
		t.Errorf("a 20-column frame rendered a bar:\n%s", bar)
	}
}

// No configured filter, no bar: there is nothing to report.
func TestNoBarWithoutAFilter(t *testing.T) {
	v := queryView(t, func(c *config.GitHubConfig) { c.Filter = "" })
	if bar := v.QueryBar(120); bar != "" {
		t.Errorf("a blank filter still rendered a bar: %q", ansi.Strip(bar))
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
