package prs

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func toggleView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil)
	v.SetSize(80, 60, 40)
	v.Update(mineMsg{page: searchPage{prs: []pr{
		{Number: 1, URL: "u1", Body: "one"},
		{Number: 2, URL: "u2", Body: "two"},
	}}})
	return v
}

// Opening a diff on one PR must not put the next PR in diff view.
func TestPaneResetsOnSelectionChange(t *testing.T) {
	v := toggleView(t)
	v.pane = paneDiff
	v.expanded = "u1"

	v.Update(tea.KeyPressMsg{Code: 'j'})

	if v.pane != paneBody {
		t.Errorf("pane = %v after moving on, want the body pane", v.pane)
	}
	if v.expanded != "" {
		t.Errorf("expanded = %q after moving on, want cleared", v.expanded)
	}
}

// Approving is the end of reviewing that PR, so the pane opened to review it
// folds away rather than following you to the next one.
func TestPaneResetsAfterReview(t *testing.T) {
	v := toggleView(t)
	v.pane = paneDiff
	v.Update(reviewDoneMsg{what: "approved o/r#1", url: "u1", state: "APPROVED"})
	if v.pane != paneBody {
		t.Errorf("pane = %v after approving, want the body pane", v.pane)
	}
}

// persist is the old behaviour, kept for anyone who wants it.
func TestPanePersistsWhenConfigured(t *testing.T) {
	v := toggleView(t)
	v.Update(ui.TogglesPersistMsg(true))
	v.pane = paneDiff
	v.Update(tea.KeyPressMsg{Code: 'j'})
	if v.pane != paneDiff {
		t.Errorf("pane = %v, want the diff pane kept when toggles persist", v.pane)
	}
}
