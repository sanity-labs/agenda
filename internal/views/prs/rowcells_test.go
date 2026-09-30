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

// The row renderer right-aligns the cluster, so a cluster that shrinks
// with its contents puts every cell at a different column. This is the
// regression that shipped: trimming the padding made the columns drift,
// which looked fine in isolation and wrong down a real list.
func TestCellsLandOnTheSameColumnDownTheList(t *testing.T) {
	const width = 176
	// Vary the age too: it is the last cell, so its padding is what a
	// trailing trim eats, and equal ages would hide the bug.
	ages := []time.Duration{2 * time.Hour, 10 * time.Hour, 96 * time.Hour,
		24 * time.Hour, 800 * time.Hour, 5 * time.Hour}
	n := 0
	mkLabelled := func(add, del, comments int, names ...string) pr {
		p := mkRow(add, del, comments)
		p.UpdatedAt = time.Now().Add(-ages[n%len(ages)])
		n++
		p.ShowLabels = true
		for _, n := range names {
			p.Labels.Nodes = append(p.Labels.Nodes, label{Name: n, Color: "3b82f6"})
		}
		return p
	}
	rows := []pr{
		mkLabelled(90, 0, 1),
		mkLabelled(215, 201, 2, "bot", "deps", "major"),
		mkLabelled(254, 0, 2),
		mkLabelled(4, 4, 2, "bot", "deps", "major", "extra"),
		mkLabelled(511, 0, 2),
		mkLabelled(1, 1, 3, "helm", "bot", "reviewbot:skim"),
	}

	var diffCol, rowW, clusterW int
	for i, p := range rows {
		line := ansi.Strip(strings.Split(p.Render(width, false, ui.Highlighter{}), "\n")[0])
		at := strings.Index(line, "+")
		if at < 0 {
			t.Fatalf("row %d has no diff cell: %q", i, line)
		}
		col := lipgloss.Width(line[:at])
		cluster := lipgloss.Width(ansi.Strip(p.rightCluster(width, false)))

		if i == 0 {
			diffCol, rowW, clusterW = col, lipgloss.Width(line), cluster
			continue
		}
		if col != diffCol {
			t.Errorf("row %d: diff starts at column %d, row 0 at %d", i, col, diffCol)
		}
		if w := lipgloss.Width(line); w != rowW {
			t.Errorf("row %d is %d wide, row 0 is %d", i, w, rowW)
		}
		if cluster != clusterW {
			t.Errorf("row %d cluster is %d wide, row 0 is %d: alignment depends"+
				" on this being constant", i, cluster, clusterW)
		}
	}
}
