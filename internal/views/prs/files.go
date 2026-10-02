package prs

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The file list is the diff as GitHub shows it: one row per file with its
// own counts, expandable to the hunks. It is also the only diff view that
// works on a large PR — the unified diff endpoint refuses past 300 files
// with a 406, while the files endpoint pages happily.

// prFile is one changed file, with the patch the API returns inline.
type prFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// filesState is the file list for one PR: the files, which are expanded,
// which are marked read, and where the cursor is.
type filesState struct {
	files    []prFile
	err      error
	done     bool
	open     map[string]bool
	reviewed map[string]bool
	sel      int
}

// filesMsg carries a fetched file list.
type filesMsg struct {
	url   string
	files []prFile
	err   error
}

// fetchFiles lists a PR's changed files. Paged, because a large PR has
// hundreds and the default page is 30.
func fetchFiles(url, repo string, num int) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("gh", "api", "--paginate",
			fmt.Sprintf("repos/%s/pulls/%d/files", repo, num),
			"--slurp").Output()
		if err != nil {
			return filesMsg{url: url, err: cmdErr(err)}
		}
		// --slurp wraps each page in an array, so the result is a list of
		// pages rather than one list of files.
		var pages [][]prFile
		if err := json.Unmarshal(out, &pages); err != nil {
			return filesMsg{url: url, err: err}
		}
		var files []prFile
		for _, page := range pages {
			files = append(files, page...)
		}
		return filesMsg{url: url, files: files}
	}
}

// fileRow is one rendered row: a file, or a line of its patch.
type fileRow struct {
	file  int    // index into filesState.files
	patch string // "" for the file's own row
	// line is the patch line's number in the new file, for matching
	// review threads to it. 0 when there is none (a hunk header, or a
	// deleted line, which no thread on the new side can anchor to).
	line int
}

// rows flattens the files and whatever is expanded into navigable rows,
// tracking each patch line's number in the new file so the threads
// anchored there can be shown beside it.
func (st *filesState) rows() []fileRow {
	var out []fileRow
	for i, f := range st.files {
		out = append(out, fileRow{file: i})
		if !st.open[f.Filename] {
			continue
		}
		n := 0
		for _, line := range strings.Split(strings.TrimRight(f.Patch, "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "@@"):
				n = hunkStart(line)
				out = append(out, fileRow{file: i, patch: line})
				continue
			case strings.HasPrefix(line, "-"):
				// Removed: no line in the new file to anchor to.
				out = append(out, fileRow{file: i, patch: line})
				continue
			}
			out = append(out, fileRow{file: i, patch: line, line: n})
			n++
		}
	}
	return out
}

// hunkStart reads the new-file start line out of a "@@ -a,b +c,d @@"
// header. Zero when it cannot be parsed, which just means no thread will
// match those lines.
func hunkStart(header string) int {
	plus := strings.Index(header, "+")
	if plus < 0 {
		return 0
	}
	rest := header[plus+1:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// renderFilesPane draws the file list: one row per file with its counts
// and a reviewed marker, the hunks of whatever is expanded beneath it.
func renderFilesPane(st *filesState, width int, focused bool, hint string,
	threads []prThread) string {
	if st.err != nil {
		return ui.Red.Render("could not list files: " + st.err.Error())
	}
	if !st.done {
		return ui.Faint.Render("Loading files…")
	}
	if len(st.files) == 0 {
		return ui.Faint.Render("No files changed.")
	}

	rows := st.rows()
	var out []string
	out = append(out, filesSummary(st))
	out = append(out, "")
	for i, r := range rows {
		f := st.files[r.file]
		if r.patch != "" {
			out = append(out, "    "+diffLine(r.patch, width-4))
			// A review thread anchored here goes under the line it is
			// about, boxed so it reads as a conversation rather than more
			// diff.
			for _, t := range threadsAt(threads, f.Filename, r.line) {
				out = append(out, threadBox(t, width-4)...)
			}
			continue
		}
		cursor := "  "
		if focused && i == st.sel {
			cursor = ui.Accent.Render("› ")
		}
		mark := " "
		if st.reviewed[f.Filename] {
			mark = ui.Green.Render(ui.Glyph(ui.IconApproved, "x"))
		}
		caret := ui.Faint.Render("▸")
		if st.open[f.Filename] {
			caret = ui.Faint.Render("▾")
		}
		counts := ui.Green.Render("+"+compactCount(f.Additions)) + " " +
			ui.Red.Render("-"+compactCount(f.Deletions))
		name := f.Filename
		if st.reviewed[f.Filename] {
			name = ui.Faint.Render(name)
		}
		// Name first, counts right: the column of counts is what the eye
		// scans for the big changes.
		room := max(1, width-lipgloss.Width(cursor+mark+caret+counts)-4)
		out = append(out, cursor+mark+caret+" "+
			ui.PadCell(ui.Truncate(name, room), room)+" "+counts)
	}
	if hint != "" {
		out = append(out, "", ui.Faint.Render(hint))
	}
	return strings.Join(out, "\n")
}

// filesSummary counts the files and how many are marked read, so progress
// through a large review is visible without scrolling.
func filesSummary(st *filesState) string {
	done := 0
	for _, f := range st.files {
		if st.reviewed[f.Filename] {
			done++
		}
	}
	var add, del int
	for _, f := range st.files {
		add, del = add+f.Additions, del+f.Deletions
	}
	s := fmt.Sprintf("%d files", len(st.files))
	if done > 0 {
		s += fmt.Sprintf(", %d of %d reviewed", done, len(st.files))
	}
	return ui.Bold.Render(s) + "  " +
		ui.Green.Render("+"+strconv.Itoa(add)) + " " +
		ui.Red.Render("-"+strconv.Itoa(del))
}

// diffLine colours one patch line the way the unified diff pane does.
func diffLine(s string, width int) string {
	s = ui.Truncate(s, max(1, width))
	switch {
	case strings.HasPrefix(s, "+"):
		return ui.Green.Render(s)
	case strings.HasPrefix(s, "-"):
		return ui.Red.Render(s)
	case strings.HasPrefix(s, "@@"):
		return ui.Cyan.Render(s)
	}
	return ui.Faint.Render(s)
}

// threadsAt returns the review threads anchored to one line of one file.
// Line 0 matches nothing: a hunk header or a removed line has no line in
// the new file for a thread to point at.
func threadsAt(threads []prThread, path string, line int) []prThread {
	if line == 0 {
		return nil
	}
	var out []prThread
	for _, t := range threads {
		if t.Path == path && t.Line != nil && *t.Line == line {
			out = append(out, t)
		}
	}
	return out
}

// threadBox renders one review thread under the line it is about: a
// bordered block, so it reads as a conversation rather than more diff.
func threadBox(t prThread, width int) []string {
	var body []string
	for _, c := range t.Comments.Nodes {
		head := ui.Cyan.Render("@" + c.Author.Login)
		if t.IsResolved {
			head += ui.Faint.Render("  resolved")
		} else if t.IsOutdated {
			head += ui.Faint.Render("  outdated")
		}
		body = append(body, head)
		for _, line := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
			body = append(body, ui.Truncate(line, max(1, width-6)))
		}
	}
	if len(body) == 0 {
		return nil
	}
	style := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Yellow)).
		Padding(0, 1).
		Width(max(10, width-4))
	if t.IsResolved {
		style = style.BorderForeground(lipgloss.Color(ui.Pal().Dim))
	}
	return strings.Split(style.Render(strings.Join(body, "\n")), "\n")
}
