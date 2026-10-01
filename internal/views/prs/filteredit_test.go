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

// Enter tries the filter before keeping it, and persists only once it is
// known to return something.
func TestEnterPersistsOnlyAfterItWorks(t *testing.T) {
	v := editView(t)
	v.Update(tea.KeyPressMsg{Code: 'F'})
	v.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	for _, r := range "author:@me -org:x" {
		v.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter produced no command: nothing was tried")
	}
	if v.filterEd != nil {
		t.Error("enter left the editor open")
	}
	// Applied to the live config so the trial uses it, but not yet written.
	if v.cfg.Filter != "author:@me -org:x" {
		t.Errorf("live config is %q, want the edited query", v.cfg.Filter)
	}

	// A successful trial is what asks for the write.
	cmd := v.Update(filterTriedMsg{path: "github.filter",
		query: "author:@me -org:x", prev: "author:@me", got: 4})
	if cmd == nil {
		t.Fatal("a working filter produced no command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("got %T, want a batch of persist + refetch", cmd())
	}
	found := false
	for _, sub := range batch {
		if msg, ok := sub().(ui.ConfigSetMsg); ok &&
			msg.Path == "github.filter" && msg.Value == "author:@me -org:x" {
			found = true
		}
	}
	if !found {
		t.Error("a working filter was not persisted")
	}
}

// A filter that matches nothing only because of its author terms is not
// saved: GitHub answers an unresolvable author with a clean zero, so a
// typo looks like "nothing matches" and persisting it would leave an
// empty list with the editor the only way back.
func TestBadAuthorFilterIsNotSaved(t *testing.T) {
	v := editView(t)
	before := v.cfg.Filter
	v.cfg.Filter = "author:@me -author:renovate" // as the trial left it

	cmd := v.Update(filterTriedMsg{
		path:    "github.filter",
		query:   "author:@me -author:renovate",
		prev:    before,
		got:     0,  // nothing with the author term
		without: 12, // but plenty without it
	})
	if v.cfg.Filter != before {
		t.Errorf("the rejected filter stuck: %q, want %q", v.cfg.Filter, before)
	}
	if cmd == nil {
		t.Fatal("no warning was raised")
	}
	msg, ok := cmd().(ui.StatusMsg)
	if !ok {
		t.Fatalf("got %T, want a status message", cmd())
	}
	if msg.Severity != ui.SeverityWarn {
		t.Errorf("severity = %v, want a warning", msg.Severity)
	}
	for _, want := range []string{"not saved", "author", "app/renovate"} {
		if !strings.Contains(msg.Summary+msg.Detail, want) {
			t.Errorf("the warning does not mention %q: %s", want, msg.Summary)
		}
	}
}

// A genuinely empty result is still saved: "no PRs match" is a valid
// answer, and only the author case is a silent failure.
func TestEmptyButValidFilterIsSaved(t *testing.T) {
	v := editView(t)
	cmd := v.Update(filterTriedMsg{path: "github.filter",
		query: "author:@me label:nothing-has-this", prev: "author:@me",
		got: 0, without: 0})
	if cmd == nil {
		t.Fatal("an empty but valid filter produced no command")
	}
	if _, ok := cmd().(ui.StatusMsg); ok {
		t.Error("an empty but valid filter was warned about")
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
	// The editor shows on the band it is editing, not in the header: the
	// change belongs with the list it will change.
	var banded bool
	for _, it := range v.list.Items() {
		if it.Separator != "" && strings.Contains(ansi.Strip(it.Separator), "█") {
			banded = true
		}
	}
	if !banded {
		t.Error("the open editor does not show on any section band")
	}
}

// After a rejection the editor still opens on the filter that works, so
// the way out is the same key that got you here: no restart, no settings.
func TestEditorStillUsableAfterARejection(t *testing.T) {
	v := editView(t)
	good := v.cfg.Filter

	v.cfg.Filter = "author:@me -author:renovate"
	v.Update(filterTriedMsg{path: "github.filter",
		query: v.cfg.Filter, prev: good, got: 0, without: 12})

	v.Update(tea.KeyPressMsg{Code: 'F'})
	if v.filterEd == nil {
		t.Fatal("'F' does not reopen the editor after a rejection")
	}
	if v.filterEd.query != good {
		t.Errorf("the editor opened on %q, want the filter that works (%q)",
			v.filterEd.query, good)
	}
}
