package prs

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The numeric cells were sized to their contents, so a row with 4-digit
// diffs shifted its neighbours and there was no column to read down.
func TestNumericCellsHoldTheirWidth(t *testing.T) {
	rows := []pr{
		mkRow(7, 3, 2), mkRow(1234, 567, 12), mkRow(45678, 9012, 345), mkRow(0, 0, 0),
	}
	var widths []int
	for _, p := range rows {
		cluster := p.rightCluster(140, false)
		// The age cell is last and fixed, so the cluster width is constant
		// once each cell is padded.
		widths = append(widths, lipgloss.Width(cluster))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("row %d cluster is %d wide, row 0 is %d: the cells drift",
				i, w, widths[0])
		}
	}
}

// Big numbers change unit rather than widening the column.
func TestCompactCount(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{
		{0, "0"}, {7, "7"}, {999, "999"},
		{1000, "1.0k"}, {1234, "1.2k"}, {9949, "9.9k"}, {9950, "10k"}, {9999, "10k"},
		{10000, "10k"}, {45678, "46k"}, {999499, "999k"},
		{1000000, "1M"}, {2500000, "3M"},
	} {
		if got := compactCount(c.in); got != c.want {
			t.Errorf("compactCount(%d) = %q, want %q", c.in, got, c.want)
		}
		if w := len(c.want); w > 5 {
			t.Errorf("compactCount(%d) = %q is %d wide, too wide for a column",
				c.in, c.want, w)
		}
	}
}

// Every compact diff pair fits the cell it is padded to, or the padding
// silently stops aligning anything.
func TestDiffCellFitsItsWidth(t *testing.T) {
	for _, n := range []int{0, 9, 99, 999, 1234, 9999, 45678, 999999, 5000000} {
		p := mkRow(n, n, 0)
		if w := lipgloss.Width(p.diffText()); w > diffCellW {
			t.Errorf("diff text for %d is %d wide, cell is %d: %q",
				n, w, diffCellW, p.diffText())
		}
	}
}

// Labels fill the freed space, and what does not fit becomes a count so
// the row still says labels exist.
func TestRowLabelsBudget(t *testing.T) {
	labels := []label{
		{Name: "infrastructure", Color: "3b82f6"},
		{Name: "needs-review", Color: "3b82f6"},
		{Name: "sre", Color: "3b82f6"},
	}
	for _, c := range []struct {
		budget   int
		wantPill string // a pill that must be present ("" for none)
		wantMark string // the overflow marker ("" for none)
	}{
		{0, "", ""},
		{6, "", "+3"}, // no pill fits: the count alone
		{30, "infrastructure", "+2"},
		{80, "sre", ""}, // all three, no marker
	} {
		got := ansi.Strip(rowLabels(labels, c.budget))
		if w := lipgloss.Width(rowLabels(labels, c.budget)); w > c.budget && c.budget > 0 {
			t.Errorf("budget %d: rendered %d wide", c.budget, w)
		}
		if c.wantPill != "" && !strings.Contains(got, c.wantPill) {
			t.Errorf("budget %d: %q missing pill %q", c.budget, got, c.wantPill)
		}
		if c.wantMark != "" && !strings.Contains(got, c.wantMark) {
			t.Errorf("budget %d: %q missing marker %q", c.budget, got, c.wantMark)
		}
		if c.wantMark == "" && strings.Contains(got, "+") && c.budget >= 80 {
			t.Errorf("budget %d: %q has a marker but everything fit", c.budget, got)
		}
	}
}

// Labels only appear when the preview is off: with a pane there is no room.
func TestLabelsOnlyWhenPreviewHidden(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(140, 0, 40)

	p := mkRow(7, 3, 2)
	p.Labels.Nodes = []label{{Name: "infra", Color: "3b82f6"}}
	v.Update(ui.PreviewShownMsg(false))
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	if !anyShowsLabels(v) {
		t.Error("labels are off with the preview hidden, want them on")
	}

	v.Update(ui.PreviewShownMsg(true))
	if anyShowsLabels(v) {
		t.Error("labels stayed on with the preview shown")
	}
}

// A narrow list keeps every column for the metadata.
func TestNarrowListHasNoLabelColumn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.Update(ui.PreviewShownMsg(false))
	v.SetSize(labelColMinWidth-1, 0, 40)

	p := mkRow(7, 3, 2)
	p.Labels.Nodes = []label{{Name: "infra", Color: "3b82f6"}}
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	if anyShowsLabels(v) {
		t.Errorf("a %d-column list got a label column", labelColMinWidth-1)
	}
}

func mkRow(add, del, comments int) pr {
	p := pr{Number: 1, URL: "u", Title: "t", Additions: add, Deletions: del,
		State: "OPEN", UpdatedAt: time.Now().Add(-48 * time.Hour)}
	p.Repository.NameWithOwner = "o/r"
	p.Comments.TotalCount = comments
	return p
}

func anyShowsLabels(v *View) bool {
	for _, p := range v.list.Items() {
		if p.URL != "" && p.ShowLabels {
			return true
		}
	}
	return false
}
