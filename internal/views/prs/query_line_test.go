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

// The query line is its own row under the status, and the list must be
// sized to leave room or the last row falls off the bottom.
func TestQueryLineGetsItsOwnRow(t *testing.T) {
	v := queryView(t, nil)
	// The box is three rows (two borders and the query), so the status plus
	// the box is four.
	if got := v.headerRows(); got != 4 {
		t.Errorf("headerRows = %d with a boxed query line, want 4", got)
	}

	lines := strings.Split(v.ListView(), "\n")
	if len(lines) < 4 {
		t.Fatalf("ListView has %d lines, want a status and a 3-row box", len(lines))
	}
	// Line 1 is the status, 2 the box's top border, 3 the query itself.
	if !strings.Contains(ansi.Strip(lines[2]), "author:@me") {
		t.Errorf("line 3 is not the query: %q", ansi.Strip(lines[2]))
	}
	if !strings.Contains(lines[1], "╭") {
		t.Errorf("line 2 is not the box's top border: %q", ansi.Strip(lines[1]))
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

// The box spans the list and never overflows it, at any width: Width() is
// the outer box, so sizing it to the full list width pushes the border a
// column past the pane.
func TestQueryBoxFitsTheList(t *testing.T) {
	for _, w := range []int{200, 120, 100, 60, 40, 31, 30, 29, 10} {
		v := queryView(t, nil)
		v.SetSize(w, 0, 30)
		q := v.queryLine()
		if q == "" {
			continue // too narrow for a box, which is allowed
		}
		for i, line := range strings.Split(q, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("list %d: box line %d is %d wide", w, i, got)
			}
		}
		// Three rows: two borders and the query. A long query that wrapped
		// would make it more.
		if h := lipgloss.Height(q); h != 3 {
			t.Errorf("list %d: box is %d rows, want 3 (a wrapped query)", w, h)
		}
		// And the list is sized to leave room for all of them.
		if got, want := v.headerRows(), 1+lipgloss.Height(q); got != want {
			t.Errorf("list %d: headerRows = %d, want %d", w, got, want)
		}
	}
}

// A query longer than the box is truncated, not wrapped: wrapping grows the
// box and eats list rows.
func TestLongQueryIsTruncated(t *testing.T) {
	v := queryView(t, func(c *config.GitHubConfig) {
		c.Filter = strings.Repeat("author:someone-with-a-long-name ", 12)
	})
	v.SetSize(80, 0, 30)
	q := v.queryLine()
	if h := lipgloss.Height(q); h != 3 {
		t.Fatalf("a long query made the box %d rows, want 3", h)
	}
	if !strings.Contains(ansi.Strip(q), "…") {
		t.Error("a truncated query has no ellipsis to say so")
	}
}

// Below a usable width the box is dropped rather than rendering a border
// with no room for the query inside it.
func TestNoBoxOnANarrowList(t *testing.T) {
	v := queryView(t, nil)
	v.SetSize(20, 0, 30)
	if q := v.queryLine(); q != "" {
		t.Errorf("a 20-column list rendered a box:\n%s", q)
	}
	if got := v.headerRows(); got != 1 {
		t.Errorf("headerRows = %d with no box, want 1", got)
	}
}
