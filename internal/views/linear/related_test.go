package linear

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/cache"
	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/store"
	"github.com/sanity-labs/agenda/internal/ui"
)

func withPR(i issue, url, title string) issue {
	var a attachment
	a.URL, a.SourceType, a.Title = url, "github", title
	i.Attachments.Nodes = append(i.Attachments.Nodes, a)
	return i
}

// The detail has a Pull requests section, one line per attached PR, open
// from the start: a review walks them, and 'l' picks one to jump to. An
// issue with none says so.
func TestPullRequestsSectionListsThePRs(t *testing.T) {
	v := floatIssue(t, 0)
	v.Update(ui.PreviewShownMsg(true))
	i := withPR(v.list.Selected(), "https://github.com/o/r/pull/5", "Fix the thing")
	v.raw = []issue{i}
	v.applySort()

	text := ansi.Strip(v.PreviewView())
	for _, want := range []string{"Pull requests", "o/r#5", "Fix the thing", "l to jump to one"} {
		if !strings.Contains(text, want) {
			t.Errorf("section is missing %q:\n%s", want, text)
		}
	}

	none := floatIssue(t, 0)
	none.Update(ui.PreviewShownMsg(true))
	if text := ansi.Strip(none.PreviewView()); !strings.Contains(text, "Pull requests") || !strings.Contains(text, "none") {
		t.Errorf("an issue without PRs should still show the section with none:\n%s", text)
	}
}

// Following a reference to an issue that is not loaded fetches it in and
// selects it; a failed fetch falls back to the browser.
func TestFetchedIssueIsAddedAndSelected(t *testing.T) {
	v := floatIssue(t, 0)
	if cmd := v.FetchRef("SRE-9", "https://linear.app/x/SRE-9"); cmd == nil {
		t.Fatal("FetchRef returned no command")
	}
	v.Update(issueFetchedMsg{id: "SRE-9", url: "u", issue: issue{Identifier: "SRE-9", Title: "fetched"}})
	if !v.HasRef("SRE-9") || v.list.Selected().Identifier != "SRE-9" {
		t.Errorf("fetched issue not added and selected: has=%v selected=%s", v.HasRef("SRE-9"), v.list.Selected().Identifier)
	}
	v.Update(issueFetchedMsg{id: "SRE-9", url: "u", issue: issue{Identifier: "SRE-9", Title: "fetched"}})
	n := 0
	for _, i := range v.raw {
		if i.Identifier == "SRE-9" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("fetching twice added the issue %d times", n)
	}
	if cmd := v.Update(issueFetchedMsg{id: "SRE-10", url: "u", err: errBoom}); cmd == nil {
		t.Error("a failed fetch did not fall back to opening the URL")
	}
}

// A ticket another view names that this list does not hold is fetched once
// and published to the store with its status, not added to the list.
func TestResolveRefsFetchesAndPublishesUnknownIssues(t *testing.T) {
	v := floatIssue(t, 0)
	s := store.New()
	v.store = s
	if cmd := v.Update(ui.ResolveRefsMsg{Kind: "linear", IDs: []string{"SRE-1"}}); cmd != nil {
		t.Error("a listed issue was fetched again")
	}
	if cmd := v.Update(ui.ResolveRefsMsg{Kind: "linear", IDs: []string{"SRE-77"}}); cmd == nil {
		t.Fatal("an unknown issue was not fetched")
	}
	if cmd := v.Update(ui.ResolveRefsMsg{Kind: "linear", IDs: []string{"SRE-77"}}); cmd != nil {
		t.Error("the same issue was fetched twice")
	}
	i := issue{Identifier: "SRE-77", Title: "Parked", URL: "u"}
	i.State.Name, i.State.Type = "Backlog", "backlog"
	v.Update(issueResolvedMsg{id: "SRE-77", issue: i})
	got, ok := s.Issue("SRE-77")
	if !ok || got.StateType != "backlog" || got.Title != "Parked" {
		t.Errorf("store record = %+v, %v; want the fetched issue with its state type", got, ok)
	}
	if v.HasRef("SRE-77") {
		t.Error("a resolved issue was added to the list")
	}
	// A failed fetch is said in the log, naming the id.
	cmd := v.Update(issueResolvedMsg{id: "SRE-78", err: errBoom})
	if cmd == nil {
		t.Fatal("a failed resolve was silent")
	}
	if st, ok := cmd().(ui.StatusMsg); !ok || !strings.Contains(st.Summary, "SRE-78") {
		t.Errorf("failed resolve reported %+v, want a status naming SRE-78", cmd())
	}
}

// A daemon fetch that found nothing still ends the loading state.
func TestEmptyCacheFromTheDaemonEndsLoading(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.loading = true
	_ = cache.Save(cacheName, []issue{{Identifier: "SRE-5", Title: "done elsewhere"}})
	v.Update(ui.CacheChangedMsg{Name: cacheName})
	if len(v.raw) != 1 {
		t.Fatalf("rows after a non-empty cache: %d, want 1", len(v.raw))
	}
	_ = cache.Save(cacheName, []issue{})
	v.Update(ui.CacheChangedMsg{Name: cacheName})
	if v.Loading() {
		t.Fatal("still loading after the daemon saved an empty cache")
	}
	if len(v.raw) != 0 {
		t.Fatalf("rows after an empty cache: %d, want 0 (the issue is gone)", len(v.raw))
	}
}

// A lookup that fails to get through says nothing about the id; only
// Linear's own "not found" marks it as not an issue.
func TestOnlyANotFoundMarksAnIdMissing(t *testing.T) {
	v := floatIssue(t, 0)
	s := store.New()
	s.PutIssues([]store.Issue{{Identifier: "SRE-1"}})
	v.store = s
	v.Update(issueResolvedMsg{id: "SRE-77", err: errBoom})
	if !s.Vouched("SRE-77") {
		t.Error("a transient error marked the id missing")
	}
	v.Update(issueResolvedMsg{id: "SRE-77", err: errors.New("linear: Entity not found: Issue")})
	if s.Vouched("SRE-77") {
		t.Error("Linear's not-found did not mark the id missing")
	}
}

// An issue's parent, sub-issues and relations list under the detail with
// their status glyph and how they relate, and 'l' offers them after the
// PRs, each once, so a jump to either needs no searching.
func TestRelatedIssuesInDetailAndPicker(t *testing.T) {
	v := floatIssue(t, 0)
	v.Update(ui.PreviewShownMsg(true))
	i := withPR(v.list.Selected(), "https://github.com/o/r/pull/5", "Fix the thing")
	parent := issueRef{Identifier: "SRE-100", Title: "Epic", URL: "u100"}
	parent.State.Type, parent.State.Name = "started", "In Progress"
	i.Parent = &parent
	child := issueRef{Identifier: "SRE-101", Title: "Part one", URL: "u101"}
	child.State.Type = "completed"
	i.Children.Nodes = []issueRef{child}
	blocker := issueRef{Identifier: "SRE-102", Title: "Waiting on this", URL: "u102"}
	blocker.State.Type = "unstarted"
	i.InverseRelations.Nodes = []inverseRelation{{Type: "blocks", Issue: blocker}}
	i.Relations.Nodes = []relation{{Type: "related", RelatedIssue: child}} // a repeat: listed once
	v.raw = []issue{i}
	v.applySort()

	text := ansi.Strip(v.PreviewView())
	for _, want := range []string{"Related issues", "SRE-100", "parent", "SRE-101", "sub-issue", "SRE-102", "blocked by"} {
		if !strings.Contains(text, want) {
			t.Errorf("detail is missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "SRE-101") != 1 {
		t.Errorf("a related issue listed twice:\n%s", text)
	}
	refs := v.Refs()
	var kinds []string
	for _, r := range refs {
		kinds = append(kinds, r.Kind+":"+r.ID)
	}
	want := "pr:https://github.com/o/r/pull/5,linear:SRE-100,linear:SRE-101,linear:SRE-102"
	if got := strings.Join(kinds, ","); got != want {
		t.Fatalf("refs = %s, want %s", got, want)
	}
	if !strings.Contains(refs[1].Detail, "parent") || !strings.Contains(refs[1].Label, ui.IconIssueInProgress) {
		t.Errorf("parent ref = %+v, want the status glyph and how it relates", refs[1])
	}
	if v.store != nil {
		if iss, ok := v.store.Issue("SRE-102"); !ok || iss.StateType != "unstarted" {
			t.Error("related issues were not published to the store")
		}
	}
}

// Relations are fetched once per selected issue, not with the list (a
// page of 100 with them was over Linear's complexity limit); when they
// land the detail shows them and the store learns them.
func TestRelatedIssuesFetchedOnSelection(t *testing.T) {
	v := floatIssue(t, 0)
	v.Update(ui.PreviewShownMsg(true))
	s := store.New()
	v.store = s
	// The load selected SRE-1, so its fetch is already in flight.
	if _, started := v.related["SRE-1"]; !started {
		t.Fatal("the selected issue's relations were not fetched")
	}
	if cmd := v.maybeFetchRelated(); cmd != nil {
		t.Fatal("the relations were fetched twice for one issue")
	}
	if strings.Contains(ansi.Strip(v.PreviewView()), "Related issues") {
		t.Fatal("a section with nothing to show yet")
	}
	i := issue{Identifier: "SRE-1"}
	rel := issueRef{Identifier: "VULN-9", Title: "A finding", URL: "u"}
	rel.State.Type, rel.State.Name = "unstarted", "Todo"
	i.Relations.Nodes = []relation{{Type: "related", RelatedIssue: rel}}
	v.Update(relatedMsg{id: "SRE-1", issue: i})
	text := ansi.Strip(v.PreviewView())
	if !strings.Contains(text, "Related issues") || !strings.Contains(text, "VULN-9") || !strings.Contains(text, "related") {
		t.Fatalf("relations that landed are not in the detail:\n%s", text)
	}
	if iss, ok := s.Issue("VULN-9"); !ok || iss.StateType != "unstarted" {
		t.Error("a related issue that landed was not published to the store")
	}
	if n := len(v.Refs()); n != 1 || v.Refs()[0].ID != "VULN-9" {
		t.Errorf("refs = %d, want the one related issue", n)
	}
}

// The relations fetch follows the selection however it moved: the first
// row after a load, a jump from another view, a repaint from the daemon.
// Only a key move used to trigger it, so 'l' on an issue reached any other
// way offered its PRs and nothing else.
func TestRelatedFetchFollowsAnySelectionChange(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	cmd := v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1"}, {Identifier: "SRE-2"}}, source: v.defaultSource})
	if _, started := v.related["SRE-1"]; !started || cmd == nil {
		t.Fatal("the first row's relations were not fetched after the load")
	}
	if !v.SelectRef("SRE-2") {
		t.Fatal("SelectRef failed")
	}
	if cmd := v.Update(ui.SelectedMsg{}); cmd == nil {
		t.Fatal("a jump to SRE-2 did not fetch its relations")
	}
	if _, started := v.related["SRE-2"]; !started {
		t.Fatal("SRE-2's relations not in flight after the jump")
	}
	if cmd := v.Update(ui.SelectedMsg{}); cmd != nil {
		t.Fatal("a second message on the same selection fetched again")
	}
	// A failed fetch is said in the log rather than swallowed.
	cmd = v.Update(relatedMsg{id: "SRE-2", err: errBoom})
	if cmd == nil {
		t.Fatal("a failed relations fetch was silent")
	}
	if st, ok := cmd().(ui.StatusMsg); !ok || !strings.Contains(st.Summary, "SRE-2") {
		t.Errorf("failed fetch reported %+v", cmd())
	}
}
