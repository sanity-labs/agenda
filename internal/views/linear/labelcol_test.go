package linear

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func issueRow(id string, age time.Duration, labels ...string) issue {
	i := issue{Identifier: id, Title: "an issue title", UpdatedAt: time.Now().Add(-age)}
	i.State.Name = "In Progress"
	i.ShowLabels = true
	for _, l := range labels {
		i.Labels.Nodes = append(i.Labels.Nodes, label{Name: l, Color: "3b82f6"})
	}
	return i
}

// The age cell and the label column have to land on the same screen
// columns down the list, or the eye has nothing to track.
func TestLinearCellsAlign(t *testing.T) {
	const width = 176
	rows := []issue{
		issueRow("SRE-1", 2*time.Hour, "infra"),
		issueRow("SRE-2", 96*time.Hour, "infra", "sre", "needs-triage"),
		issueRow("SRE-3", 800*time.Hour),
		issueRow("SRE-4", 10*time.Hour, "a", "b", "c", "d", "e"),
	}
	var rowW int
	for i, is := range rows {
		line := ansi.Strip(strings.Split(is.Render(width, false, ui.Highlighter{}), "\n")[0])
		if i == 0 {
			rowW = lipgloss.Width(line)
			continue
		}
		if w := lipgloss.Width(line); w != rowW {
			t.Errorf("row %d is %d wide, row 0 is %d: the cells drift", i, w, rowW)
		}
	}
}

// Labels only when the preview is off and the list is wide enough.
func TestLinearLabelsFollowThePreview(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(176, 0, 40)
	v.Update(ui.PreviewShownMsg(false))
	v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1", Title: "t"}}, source: v.defaultSource})

	if !anyLabels(v) {
		t.Error("labels are off with the preview hidden, want them on")
	}
	v.Update(ui.PreviewShownMsg(true))
	if anyLabels(v) {
		t.Error("labels stayed on with the preview shown")
	}

	// Narrow: the metadata needs every column.
	v.Update(ui.PreviewShownMsg(false))
	v.SetSize(ui.LabelColMinRow-1, 0, 40)
	if anyLabels(v) {
		t.Errorf("a %d-column list got a label column", ui.LabelColMinRow-1)
	}
}

func anyLabels(v *View) bool {
	for _, i := range v.list.Items() {
		if i.Identifier != "" && i.ShowLabels {
			return true
		}
	}
	return false
}
