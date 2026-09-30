package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func paneView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// SummaryLines matters: with truncation off no expand hint renders, and
	// the hint is what a click targets.
	v := New(config.GitHubConfig{DiffPane: true, SummaryLines: 10}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	p := pr{Number: 1, URL: "u", Title: "t"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	return v
}

// Toggling a pane off returns to the description. With the detail floated,
// concealing instead shut the whole window, so 'c' twice (or 'd' twice)
// closed the float rather than going back to the summary.
func TestPaneToggleOffKeepsTheDetailOpen(t *testing.T) {
	for _, key := range []rune{'c', 'd'} {
		v := paneView(t)
		v.Update(ui.PreviewShownMsg(true)) // the detail is on screen
		v.Update(tea.KeyPressMsg{Code: key})
		if v.pane == paneBody {
			t.Fatalf("%q did not open a pane", key)
		}
		cmd := v.Update(tea.KeyPressMsg{Code: key})
		if v.pane != paneBody {
			t.Errorf("%q twice did not return to the description", key)
		}
		if cmd == nil {
			continue
		}
		if _, ok := cmd().(ui.ConcealPreviewMsg); ok {
			t.Errorf("%q twice concealed the detail instead of showing the description", key)
		}
	}
}

// With the detail not on screen, toggling a pane off still puts it away:
// the pane was only revealed to show the diff or comments.
func TestPaneToggleOffConcealsARevealedPane(t *testing.T) {
	v := paneView(t)
	v.Update(ui.PreviewShownMsg(false))
	v.Update(tea.KeyPressMsg{Code: 'c'})
	cmd := v.Update(tea.KeyPressMsg{Code: 'c'})
	if cmd == nil {
		t.Fatal("toggling off sent nothing, want a conceal")
	}
	if _, ok := cmd().(ui.ConcealPreviewMsg); !ok {
		t.Errorf("toggling off sent %#v, want ConcealPreviewMsg", cmd())
	}
}

// Clicking the expand hint toggles the description, so the mouse can do
// what 'e' does.
func TestClickingTheExpandHintToggles(t *testing.T) {
	v := paneView(t)
	long := pr{Number: 1, URL: "u", Title: "t", Body: strings.Repeat("a paragraph of text.\n\n", 40)}
	long.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{long}}})

	line := hintLine(t, v, expandMarker)
	v.ClickPreview(line, 0)
	if v.expanded != "u" {
		t.Fatalf("clicking the expand hint did not expand: expanded = %q", v.expanded)
	}
	// Expanded, the hint is still there and clicking it collapses again.
	v.ClickPreview(hintLine(t, v, expandMarker), 0)
	if v.expanded != "" {
		t.Errorf("clicking again did not collapse: expanded = %q", v.expanded)
	}
}

// Clicking the comments hint opens the comments pane.
func TestClickingTheCommentsHintOpensComments(t *testing.T) {
	v := paneView(t)
	p := pr{Number: 1, URL: "u", Title: "t"}
	p.Repository.NameWithOwner = "o/r"
	p.Comments.TotalCount = 3
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})

	v.ClickPreview(hintLine(t, v, commentsMarker), 0)
	if v.pane != paneComments {
		t.Errorf("pane = %v after clicking the comments hint, want comments", v.pane)
	}
}

// A click on ordinary text does nothing: only the hints are targets.
func TestClickingElsewhereDoesNothing(t *testing.T) {
	v := paneView(t)
	before := v.expanded
	v.ClickPreview(0, 0) // the title line
	if v.expanded != before || v.pane != paneBody {
		t.Error("a click on the title changed the view")
	}
	// Out of range is ignored rather than panicking.
	v.ClickPreview(-1, 0)
	v.ClickPreview(1<<20, 0)
}

// hintLine is the preview line carrying marker.
func hintLine(t *testing.T, v *View, marker string) int {
	t.Helper()
	for i, l := range strings.Split(v.PreviewView(), "\n") {
		if strings.Contains(l, marker) {
			return i
		}
	}
	t.Fatalf("no preview line contains %q:\n%s", marker, v.PreviewView())
	return -1
}
