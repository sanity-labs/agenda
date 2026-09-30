package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func editView(t *testing.T) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	show := true
	cfg := config.Default().GitHub
	cfg.ShowReviewRequested = &show
	v := New(cfg, nil, nil, nil)
	v.SetSize(120, 0, 30)

	mine := pr{Number: 1, URL: "mine", Title: "t", State: "OPEN"}
	mine.Repository.NameWithOwner = "o/r"
	rev := pr{Number: 2, URL: "rev", Title: "t", State: "OPEN"}
	rev.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{mine}}})
	v.Update(reviewListMsg{page: searchPage{prs: []pr{rev}}})
	return v
}

// 'F' edits the search that produced the row you are on, so the cursor
// says which of the two you mean without another prompt.
func TestEditFilterFollowsTheSection(t *testing.T) {
	v := editView(t)

	// Walk to a row in the review section.
	for i := 0; i < 20 && !v.inReviewSection(); i++ {
		v.Update(tea.KeyPressMsg{Code: 'j'})
	}
	if !v.inReviewSection() {
		t.Fatal("never reached the review section")
	}
	v.Update(tea.KeyPressMsg{Code: 'F'})
	if v.filterEd == nil {
		t.Fatal("'F' did not open the editor")
	}
	if v.filterEd.path != "github.review_filter" {
		t.Errorf("editing %q from the review section, want review_filter",
			v.filterEd.path)
	}
	if !strings.Contains(v.filterEd.query, "review-requested") {
		t.Errorf("editor prefilled with %q, want the review search",
			v.filterEd.query)
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	// And back in the own-PRs section.
	for i := 0; i < 20 && v.inReviewSection(); i++ {
		v.Update(tea.KeyPressMsg{Code: 'k'})
	}
	v.Update(tea.KeyPressMsg{Code: 'F'})
	if v.filterEd == nil {
		t.Fatal("'F' did not open the editor in the own-PRs section")
	}
	if v.filterEd.path != "github.filter" {
		t.Errorf("editing %q from the own-PRs section, want github.filter",
			v.filterEd.path)
	}
}

// Enter applies the edit: it refetches with the new query and asks the
// root model to persist it, or a filter would need retyping every run.
func TestEnterAppliesAndPersists(t *testing.T) {
	v := editView(t)
	v.Update(tea.KeyPressMsg{Code: 'F'})
	v.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) // clear
	for _, r := range "author:@me -org:x" {
		v.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if v.filterEd != nil {
		t.Error("enter left the editor open")
	}
	if v.cfg.Filter != "author:@me -org:x" {
		t.Errorf("live config is %q, want the edited query", v.cfg.Filter)
	}
	if cmd == nil {
		t.Fatal("enter produced no command: nothing refetched or persisted")
	}
	// The batch carries a ConfigSetMsg so the root model writes it. Only
	// the top level is inspected: running every command would shell out to
	// gh for the refetch, which is not what this test is about.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("enter returned %T, want a batch of persist + refetch", cmd())
	}
	found := false
	for _, sub := range batch {
		if msg, ok := sub().(ui.ConfigSetMsg); ok &&
			msg.Path == "github.filter" && msg.Value == "author:@me -org:x" {
			found = true
			break
		}
	}
	if !found {
		t.Error("enter did not ask the root model to persist the filter")
	}
}

// Esc leaves both the config and the list alone.
func TestEscCancelsTheEdit(t *testing.T) {
	v := editView(t)
	before := v.cfg.Filter
	v.Update(tea.KeyPressMsg{Code: 'F'})
	for _, r := range "nonsense" {
		v.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if v.filterEd != nil {
		t.Error("esc left the editor open")
	}
	if v.cfg.Filter != before {
		t.Errorf("esc changed the filter to %q", v.cfg.Filter)
	}
}

// While editing, the view captures keys: a stray 'q' must type, not quit.
func TestEditorCapturesInput(t *testing.T) {
	v := editView(t)
	if v.InputActive() {
		t.Fatal("input reported active with nothing open")
	}
	v.Update(tea.KeyPressMsg{Code: 'F'})
	if !v.InputActive() {
		t.Error("the open editor does not capture input; global keys would fire")
	}
	if line := ansi.Strip(v.ListView()); !strings.Contains(line, "my PRs") {
		t.Errorf("the header does not show the editor: %q",
			strings.Split(line, "\n")[0])
	}
}
