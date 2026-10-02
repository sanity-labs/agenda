package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
)

func filesView(t *testing.T, files ...prFile) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// diff_pane opts into the in-pane view, which is the file list.
	// Auto-expand off: these cover the manual +/- behaviour, and the
	// following-cursor case has its own test.
	off := false
	v := New(config.GitHubConfig{DiffPane: true, FileAutoExpand: &off}, nil, nil, nil)
	v.SetSize(60, 70, 24)
	p := pr{Number: 1, URL: "u", Title: "t", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(tea.KeyPressMsg{Code: 'd'})
	v.Update(filesMsg{url: "u", files: files})
	v.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // focus the pane
	return v
}

func sample() []prFile {
	return []prFile{
		{Filename: "a.go", Additions: 13, Deletions: 13, Patch: "@@ -1 +1 @@\n-old\n+new"},
		{Filename: "b.go", Additions: 2400, Deletions: 2, Patch: "@@ -1 +1 @@\n+x"},
		{Filename: "c.go", Additions: 1, Patch: "@@ -1 +1 @@\n+y"},
	}
}

// Expanding shows a file's hunks inline, and the cursor skips over them:
// patch lines are content, not targets.
func TestExpandAndCollapse(t *testing.T) {
	v := filesView(t, sample()...)
	st := v.files["u"]

	v.Update(tea.KeyPressMsg{Code: '+'})
	if !st.open["a.go"] {
		t.Fatal("+ did not expand the file")
	}
	if got := len(st.rows()); got != 3+3 {
		t.Errorf("rows = %d, want 3 files plus 3 patch lines", got)
	}

	// Down lands on the next file, not the patch lines between.
	v.Update(tea.KeyPressMsg{Code: 'j'})
	rows := st.rows()
	if rows[st.sel].patch != "" {
		t.Errorf("the cursor landed on a patch line: %q", rows[st.sel].patch)
	}
	if name := st.files[rows[st.sel].file].Filename; name != "b.go" {
		t.Errorf("cursor is on %q, want b.go", name)
	}

	// And collapsing puts the rows back.
	v.Update(tea.KeyPressMsg{Code: 'k'})
	v.Update(tea.KeyPressMsg{Code: '-'})
	if st.open["a.go"] {
		t.Error("- did not collapse the file")
	}
	if got := len(st.rows()); got != 3 {
		t.Errorf("rows = %d after collapsing, want 3", got)
	}
}

// Space marks a file read and moves on, so working down a large PR is one
// key per file, as the GitHub UI does.
func TestSpaceMarksReviewedAndAdvances(t *testing.T) {
	v := filesView(t, sample()...)
	st := v.files["u"]

	v.Update(tea.KeyPressMsg{Code: ' '})
	if !st.reviewed["a.go"] {
		t.Fatal("space did not mark the file reviewed")
	}
	if name := st.files[st.rows()[st.sel].file].Filename; name != "b.go" {
		t.Errorf("after marking, the cursor is on %q, want b.go", name)
	}

	// Unmarking stays put: only marking advances.
	v.Update(tea.KeyPressMsg{Code: 'k'})
	v.Update(tea.KeyPressMsg{Code: ' '})
	if st.reviewed["a.go"] {
		t.Error("space did not unmark a reviewed file")
	}
	if name := st.files[st.rows()[st.sel].file].Filename; name != "a.go" {
		t.Errorf("unmarking moved the cursor to %q", name)
	}
}

// The summary counts progress, so a large review shows how far along it is
// without scrolling the pane.
func TestSummaryCountsReviewed(t *testing.T) {
	v := filesView(t, sample()...)
	if got := ansi.Strip(v.PreviewView()); !strings.Contains(got, "3 files") {
		t.Errorf("the summary does not count the files:\n%s", got)
	}
	v.Update(tea.KeyPressMsg{Code: ' '})
	if got := ansi.Strip(v.PreviewView()); !strings.Contains(got, "1 of 3 reviewed") {
		t.Errorf("the summary does not count progress:\n%s", got)
	}
}

// Left collapses first, then returns focus: one key walking back out,
// rather than leaving the pane with files still expanded.
func TestLeftCollapsesThenReleasesFocus(t *testing.T) {
	v := filesView(t, sample()...)
	st := v.files["u"]
	v.Update(tea.KeyPressMsg{Code: '+'})

	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if st.open["a.go"] {
		t.Error("left did not collapse the expanded file")
	}
	if !v.PaneFocused() {
		t.Error("left released focus while a file was still expanded")
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if v.PaneFocused() {
		t.Error("left did not release focus once nothing was expanded")
	}
}

// Counts line up in their own column, whatever the path lengths, and a
// big number switches unit rather than widening it.
func TestFileRowsAlign(t *testing.T) {
	v := filesView(t,
		prFile{Filename: "a.go", Additions: 1, Deletions: 1},
		prFile{Filename: "internal/views/prs/a/very/long/path/file.go",
			Additions: 45678, Deletions: 9012},
	)
	var widths []int
	for _, line := range strings.Split(ansi.Strip(v.PreviewView()), "\n") {
		if strings.Contains(line, ".go") && strings.Contains(line, "+") {
			widths = append(widths, lipgloss.Width(line))
		}
	}
	if len(widths) < 2 {
		t.Fatalf("expected two file rows, found %d", len(widths))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("row %d is %d wide, row 0 is %d: the counts drift",
				i, w, widths[0])
		}
	}
}

// A PR with no files says so rather than rendering an empty pane.
func TestNoFiles(t *testing.T) {
	v := filesView(t)
	if got := ansi.Strip(v.PreviewView()); !strings.Contains(got, "No files changed") {
		t.Errorf("an empty file list says nothing:\n%s", got)
	}
}

// On by default the list follows the cursor: the file under it opens and
// the one you left closes, so the pane stays short enough to see the list
// around it.
func TestAutoExpandFollowsTheCursor(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{DiffPane: true}, nil, nil, nil) // default on
	v.SetSize(60, 70, 24)
	p := pr{Number: 1, URL: "u", Title: "t", State: "OPEN"}
	p.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p}}})
	v.Update(tea.KeyPressMsg{Code: 'd'})
	v.Update(filesMsg{url: "u", files: sample()})
	st := v.files["u"]

	v.Update(tea.KeyPressMsg{Code: 'j'})
	if !st.open["b.go"] {
		t.Error("moving to a file did not open it")
	}
	if st.open["a.go"] {
		t.Error("the file left behind stayed open")
	}
	v.Update(tea.KeyPressMsg{Code: 'j'})
	if !st.open["c.go"] || st.open["b.go"] {
		t.Errorf("the cursor is not the only file open: %v", st.open)
	}
}
