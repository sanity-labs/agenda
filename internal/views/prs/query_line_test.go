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

// Each section's band names the search that produced it, so the two are
// read together rather than joined into one line somewhere else.
func TestBandNamesItsOwnSearch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	show := true
	cfg := config.Default().GitHub
	cfg.ShowReviewRequested = &show
	v := New(cfg, nil, nil, nil)
	v.SetSize(160, 0, 30)

	mine := pr{Number: 1, URL: "mine", Title: "t", State: "OPEN"}
	mine.Repository.NameWithOwner = "o/r"
	rev := pr{Number: 2, URL: "rev", Title: "t", State: "OPEN"}
	rev.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{mine}}})
	v.Update(reviewListMsg{page: searchPage{prs: []pr{rev}}})

	var bands []string
	for _, p := range v.list.Items() {
		if p.Separator != "" {
			bands = append(bands, ansi.Strip(p.Separator))
		}
	}
	if len(bands) != 2 {
		t.Fatalf("got %d bands, want one per section: %q", len(bands), bands)
	}
	if !strings.Contains(bands[0], "author:@me") {
		t.Errorf("the own-PRs band does not name its search: %q", bands[0])
	}
	if !strings.Contains(bands[1], "review-requested") {
		t.Errorf("the review band does not name its search: %q", bands[1])
	}
	// Each names only its own, or the two are being conflated again.
	if strings.Contains(bands[0], "review-requested") {
		t.Errorf("the own-PRs band names the review search: %q", bands[0])
	}
}

// The counts are what the band is for, so a long query gives way rather
// than pushing them out.
func TestBandKeepsItsCountsWhenNarrow(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.Default().GitHub, nil, nil, nil)

	label := "MY PULL REQUESTS  ·  8 of 9"
	long := strings.Repeat("author:someone-long ", 10)
	for _, w := range []int{200, 120, 80, 60, 40, 20, 10} {
		v.SetSize(w, 0, 30)
		band := ansi.Strip(v.bandWithQuery(label, long))
		if !strings.Contains(band, "8 of 9") {
			t.Errorf("width %d: the query pushed the counts out: %q", w, band)
		}
		// SectionSeparator pads a space each side, so the band has to fit
		// inside that or it clips the label instead.
		// The query must not push the band past the pane. A label longer
		// than the pane is SectionSeparator's problem, not this one, so
		// only check the widths where the label itself fits.
		if lipgloss.Width(label) <= w-2 {
			if got := lipgloss.Width(band); got > w-2 {
				t.Errorf("width %d: band is %d wide, want at most %d", w, got, w-2)
			}
		}
	}
}

// No filter configured, no band change: nothing to report.
func TestBandWithoutAFilter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.Default().GitHub, nil, nil, nil)
	v.SetSize(160, 0, 30)
	if got := v.bandWithQuery("MY PULL REQUESTS", ""); got != "MY PULL REQUESTS" {
		t.Errorf("a blank filter changed the band: %q", got)
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

// The query starts near the band's midpoint: away from the counts, but
// not stranded at the far edge of a wide terminal.
func TestBandQueryIsCentred(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.Default().GitHub, nil, nil, nil)
	label := "REVIEW REQUESTED  ·  1 of 83"
	q := "review-requested:@me is:open"

	for _, w := range []int{140, 100, 70, 50} {
		v.SetSize(w, 0, 30)
		band := ansi.Strip(v.bandWithQuery(label, q))
		// SectionSeparator pads a space each side, so the band must never
		// exceed one column short of the pane.
		if got := lipgloss.Width(band); got > w-2 {
			t.Errorf("width %d: band is %d wide, want at most %d", w, got, w-2)
		}
		if !strings.HasPrefix(band, label) {
			t.Errorf("width %d: the label is not first: %q", w, band)
		}
		// A run of spaces between the two halves, not a separator glyph.
		rest := strings.TrimPrefix(band, label)
		if !strings.HasPrefix(rest, strings.Repeat(" ", bandGap)) {
			t.Errorf("width %d: the halves are not spaced apart: %q", w, rest)
		}
		// The query starts at the midpoint, or straight after the label
		// plus its gap when the label is longer than half the band.
		at := lipgloss.Width(band) - lipgloss.Width(strings.TrimLeft(rest, " "))
		want := max(lipgloss.Width(label)+bandGap, (w-2)/2)
		if at != want {
			t.Errorf("width %d: query starts at %d, want %d", w, at, want)
		}
	}
}

// Too little room and the query drops entirely rather than showing an
// ellipsis that says less than nothing.
func TestBandDropsTheQueryWhenCramped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.Default().GitHub, nil, nil, nil)
	label := "REVIEW REQUESTED  ·  1 of 83"
	v.SetSize(lipgloss.Width(label)+bandGap+4, 0, 30)
	if got := v.bandWithQuery(label, "review-requested:@me"); got != label {
		t.Errorf("a cramped band still carried a query: %q", ansi.Strip(got))
	}
}
