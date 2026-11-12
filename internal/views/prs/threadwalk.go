package prs

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// threadWalk is the "Resolve threads" popup: the PR's unresolved review
// threads one at a time. Resolved threads leave the list, so idx always
// points at the next one to deal with.
type threadWalk struct {
	url, repo string
	num       int
	threads   []prThread
	idx       int
	loading   bool
	busy      bool
	err       string
	resolved  int
}

type threadResolvedMsg struct {
	from *View
	url  string
	id   string
	err  error
}

// resolveThread is swapped out in tests, which must never resolve a real
// thread.
var resolveThread = func(id string) error {
	out, err := exec.Command("gh", "api", "graphql",
		"-f", "query="+resolveMutation, "-f", "thread="+id).CombinedOutput()
	if err != nil {
		return ghErr(err, out)
	}
	return nil
}

// unresolved is the open threads in file then line order; outdated threads
// have no line and sort last within their file.
func unresolved(all []prThread) []prThread {
	var out []prThread
	for _, t := range all {
		if !t.IsResolved {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if (a.Line == nil) != (b.Line == nil) {
			return b.Line == nil
		}
		return a.Line != nil && *a.Line < *b.Line
	})
	return out
}

func (v *View) openWalk(p pr) tea.Cmd {
	w := &threadWalk{url: p.URL, repo: p.repo(), num: p.Number}
	v.walk = w
	switch st := v.comments[p.URL]; {
	case st == nil:
		if v.comments == nil {
			v.comments = map[string]*commentsState{}
		}
		v.comments[p.URL] = &commentsState{}
		w.loading = true
		return v.fetchCommentsCmd(p)
	case !st.done:
		w.loading = true
	case st.err != nil:
		w.err = st.err.Error()
	default:
		w.threads = unresolved(st.data.ReviewThreads.Nodes)
	}
	return nil
}

// walkLoaded fills a popup that was waiting on the fetch, and tells an open
// palette its commands may have changed.
func (v *View) walkLoaded(url string) tea.Cmd {
	if w := v.walk; w != nil && w.loading && w.url == url {
		w.loading = false
		if st := v.comments[url]; st.err != nil {
			w.err = st.err.Error()
		} else {
			w.threads = unresolved(st.data.ReviewThreads.Nodes)
		}
	}
	return func() tea.Msg { return ui.CommandsChangedMsg{} }
}

func (v *View) updateWalk(msg tea.KeyMsg) tea.Cmd {
	w := v.walk
	k := msg.String()
	if w.err != "" || (len(w.threads) == 0 && !w.loading) {
		v.walk = nil // the outcome, or "all resolved": any key closes
		return nil
	}
	if k == "esc" || k == "left" { // DESIGN.md: ← closes a float
		v.walk = nil
		return nil
	}
	if w.loading || w.busy {
		return nil
	}
	switch k {
	case "n":
		w.idx = min(w.idx+1, len(w.threads)-1)
	case "p":
		w.idx = max(w.idx-1, 0)
	case "o":
		t := w.threads[w.idx]
		if len(t.Comments.Nodes) > 0 && t.Comments.Nodes[0].URL != "" {
			return ui.OpenURL(t.Comments.Nodes[0].URL)
		}
		return ui.OpenURL(w.url)
	case "r":
		w.busy = true
		id := w.threads[w.idx].ID
		return func() tea.Msg {
			return threadResolvedMsg{from: v, url: w.url, id: id, err: resolveThread(id)}
		}
	}
	return nil
}

func (v *View) walkResolved(msg threadResolvedMsg) tea.Cmd {
	if v.foreign(msg.from) {
		return nil
	}
	w := v.walk
	if w == nil || w.url != msg.url {
		return nil
	}
	w.busy = false
	if msg.err != nil {
		w.err = "resolve failed: " + msg.err.Error()
		return statusCmd(ui.SeverityError, msg.err)
	}
	if st := v.comments[msg.url]; st != nil {
		for i := range st.data.ReviewThreads.Nodes {
			if st.data.ReviewThreads.Nodes[i].ID == msg.id {
				st.data.ReviewThreads.Nodes[i].IsResolved = true
			}
		}
		v.commentsRev++
	}
	for i, t := range w.threads {
		if t.ID == msg.id {
			w.threads = append(w.threads[:i], w.threads[i+1:]...)
			break
		}
	}
	w.resolved++
	w.idx = min(w.idx, max(len(w.threads)-1, 0))
	return nil
}

func (v *View) walkOverlay() string {
	w := v.walk
	width := min(76, max(30, v.listW+v.prevW-8))
	var b strings.Builder
	b.WriteString(ui.Bold.Render(fmt.Sprintf("Resolve threads %s#%d", w.repo, w.num)) + "\n\n")
	switch {
	case w.loading:
		b.WriteString(ui.SpinnerFrame(v.spinFrame) + " " + ui.Faint.Render("loading threads…"))
	case w.err != "":
		b.WriteString(ui.Red.Render("✗ "+ui.Truncate(w.err, width)) + "\n\n" + ui.Dim.Render("any key to close"))
	case len(w.threads) == 0:
		b.WriteString(ui.Green.Render("✓ All threads resolved") + "\n\n" + ui.Dim.Render("any key to close"))
	default:
		t := w.threads[w.idx]
		head := fmt.Sprintf("%d/%d ", w.idx+1, len(w.threads)) + t.Path + lineSuffix(t)
		if t.IsOutdated {
			b.WriteString(ui.Bold.Render(ui.Truncate(head, width-9)) + ui.Yellow.Render(" outdated"))
		} else {
			b.WriteString(ui.Bold.Render(ui.Truncate(head, width)))
		}
		b.WriteString("\n")
		// Long threads are cut so the popup never outgrows the screen.
		budget := max(4, v.height-10)
		var lines []string
		for _, c := range t.Comments.Nodes {
			lines = append(lines, "", ui.Cyan.Render("@"+c.Author.Login)+" "+ui.Dim.Render(ui.Age(c.CreatedAt)))
			lines = append(lines, strings.Split(strings.TrimRight(ui.Markdown(c.Body, width), "\n"), "\n")...)
		}
		if len(lines) > budget {
			lines = append(lines[:budget-1], ui.Faint.Render("…"))
		}
		b.WriteString(strings.Join(lines, "\n") + "\n\n")
		if w.busy {
			b.WriteString(ui.SpinnerFrame(v.spinFrame) + " " + ui.Faint.Render("resolving…"))
		} else {
			b.WriteString(ui.Dim.Render(ui.Truncate("r resolve · n next · p prev · o open · esc close", width)))
		}
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Width(width + 6).
		Render(b.String())
}
