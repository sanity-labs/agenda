package linear

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/ui"
)

func withPR(i issue, url, title string) issue {
	var a attachment
	a.URL, a.SourceType, a.Title = url, "github", title
	i.Attachments.Nodes = append(i.Attachments.Nodes, a)
	return i
}

// The detail has a Pull requests section: folded to a count by default,
// expanded by 'l' with the pane focused (ToggleRelated) or a click on the
// hint, one line per attached PR. An issue with none says so.
func TestPullRequestsSectionFoldsAndExpands(t *testing.T) {
	v := floatIssue(t, 0)
	v.Update(ui.PreviewShownMsg(true))
	i := withPR(v.list.Selected(), "https://github.com/o/r/pull/5", "Fix the thing")
	v.raw = []issue{i}
	v.applySort()

	text := ansi.Strip(v.PreviewView())
	if !strings.Contains(text, "Pull requests") || !strings.Contains(text, "1 · l or click to show pull requests") {
		t.Fatalf("folded section missing or wrong:\n%s", text)
	}
	if strings.Contains(text, "o/r#5") {
		t.Error("the PR row shows while the section is folded")
	}

	v.ToggleRelated()
	text = ansi.Strip(v.PreviewView())
	if !strings.Contains(text, "o/r#5") || !strings.Contains(text, "Fix the thing") {
		t.Errorf("expanded section lacks the PR:\n%s", text)
	}

	lines := strings.Split(text, "\n")
	v.ToggleRelated() // fold again, then click the hint
	for n, l := range strings.Split(ansi.Strip(v.PreviewView()), "\n") {
		if strings.Contains(l, prsMarker) {
			v.ClickPreview(n, 0)
		}
	}
	if !v.showPRs {
		t.Error("clicking the hint did not expand the section")
	}
	_ = lines

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
