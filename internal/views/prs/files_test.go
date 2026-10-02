package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
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
	// Beside a visible list, left then hands the keys back. Floated there
	// is nothing to hand them to, so esc is the way out instead.
	v.Update(ui.PreviewShownMsg(true))
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

// Patch lines carry their number in the new file, which is what review
// threads anchor to. Removed lines have none: there is nothing on the new
// side for a thread to point at.
func TestPatchLineNumbers(t *testing.T) {
	st := &filesState{open: map[string]bool{"main.go": true}, files: []prFile{{
		Filename: "main.go",
		Patch:    "@@ -10,3 +10,4 @@\n ctx := ...\n-old()\n+new()\n+more()",
	}}}
	var got []int
	for _, r := range st.rows() {
		if r.patch != "" {
			got = append(got, r.line)
		}
	}
	want := []int{0, 10, 0, 11, 12} // header, context, removed, two added
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d = %d, want %d", i, got[i], want[i])
		}
	}
}

// A thread shows under the line it is about, boxed so it reads as a
// conversation rather than more diff.
func TestThreadsRenderUnderTheirLine(t *testing.T) {
	st := &filesState{done: true, open: map[string]bool{"main.go": true}, files: []prFile{{
		Filename: "main.go",
		Patch:    "@@ -10,2 +10,2 @@\n+first()\n+second()",
	}}}
	line := 11
	threads := []prThread{{Path: "main.go", Line: &line}}
	threads[0].Comments.Nodes = []prComment{{Body: "allocates every call"}}
	threads[0].Comments.Nodes[0].Author.Login = "armandocerna"

	out := ansi.Strip(renderFilesPane(st, 70, true, "", threads))
	lines := strings.Split(out, "\n")

	at := -1
	for i, l := range lines {
		if strings.Contains(l, "second()") {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the anchored line is missing:\n%s", out)
	}
	// The box opens on the very next row, not somewhere else in the pane.
	if !strings.Contains(lines[at+1], "╭") {
		t.Errorf("no thread box under the line it is about:\n%s", out)
	}
	if !strings.Contains(out, "allocates every call") {
		t.Errorf("the thread body is missing:\n%s", out)
	}
}

// A thread on a file that is collapsed, or on a line that is not shown,
// stays out of the way rather than floating free.
func TestThreadsOnlyShowWhereTheyAnchor(t *testing.T) {
	st := &filesState{done: true, open: map[string]bool{}, files: []prFile{{
		Filename: "main.go", Patch: "@@ -1,1 +1,1 @@\n+x",
	}}}
	line := 1
	threads := []prThread{{Path: "main.go", Line: &line}}
	threads[0].Comments.Nodes = []prComment{{Body: "a remark"}}

	if out := ansi.Strip(renderFilesPane(st, 70, true, "", threads)); strings.Contains(out, "a remark") {
		t.Errorf("a thread showed on a collapsed file:\n%s", out)
	}

	// Expanded but anchored to a line this patch does not contain.
	st.open["main.go"] = true
	elsewhere := 99
	threads[0].Line = &elsewhere
	if out := ansi.Strip(renderFilesPane(st, 70, true, "", threads)); strings.Contains(out, "a remark") {
		t.Errorf("a thread showed on a line it is not anchored to:\n%s", out)
	}
}

// Moving the cursor asks the preview to scroll with it. Without this an
// expanded file taller than the pane pushes the next one off screen and
// the cursor appears to stop moving.
func TestCursorAsksThePreviewToFollow(t *testing.T) {
	v := filesView(t, sample()...)
	v.pendingJump = nil

	v.Update(tea.KeyPressMsg{Code: 'j'})
	if v.pendingJump == nil {
		t.Fatal("moving the cursor asked for no scroll")
	}
	first := *v.pendingJump

	v.pendingJump = nil
	v.Update(tea.KeyPressMsg{Code: 'j'})
	if v.pendingJump == nil {
		t.Fatal("the second move asked for no scroll")
	}
	// Clamped at the top until the cursor is far enough down, so compare
	// only once both are past the clamp.
	if *v.pendingJump < first {
		t.Errorf("the scroll went backwards: %d then %d", first, *v.pendingJump)
	}
}
