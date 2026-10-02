package prs

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// PR comments: the top-level conversation, review summaries, and inline
// review threads. Fetched on demand (entering the comments or diff pane) and
// cached per PR URL; threads render as annotations pinned into the diff.

type prAuthor struct {
	Login string `json:"login"`
}

type prComment struct {
	ID        string    `json:"id"`
	Author    prAuthor  `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type prReview struct {
	Author    prAuthor  `json:"author"`
	State     string    `json:"state"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type prThread struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Path       string `json:"path"`
	Line       *int   `json:"line"`
	Comments   struct {
		Nodes []prComment `json:"nodes"`
	} `json:"comments"`
}

// prComments is everything the comments pane shows for one PR.
type prComments struct {
	Comments struct {
		Nodes []prComment `json:"nodes"`
	} `json:"comments"`
	Reviews struct {
		Nodes []prReview `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		Nodes []prThread `json:"nodes"`
	} `json:"reviewThreads"`
}

// commentsState tracks one PR's comment fetch.
type commentsState struct {
	data prComments
	err  error
	done bool
}

type commentsMsg struct {
	url  string
	data prComments
	err  error
}

const commentsQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      comments(first: 50) { nodes { id author { login } body createdAt } }
      reviews(first: 50) { nodes { author { login } state body createdAt } }
      reviewThreads(first: 100) {
        nodes {
          id isResolved isOutdated path line
          comments(first: 50) { nodes { id author { login } body createdAt } }
        }
      }
    }
  }
}`

// maybeFetchComments starts a comment fetch for the selected PR when a pane
// that needs them is showing and nothing is cached or in flight.
func (v *View) maybeFetchComments() tea.Cmd {
	// The file list needs them too, for the threads it shows beside the
	// lines they are about.
	if v.pane != paneDiff && v.pane != paneComments && v.pane != paneFiles {
		return nil
	}
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	if _, started := v.comments[p.URL]; started {
		return nil
	}
	if v.comments == nil {
		v.comments = map[string]*commentsState{}
	}
	v.comments[p.URL] = &commentsState{} // in flight
	return v.fetchCommentsCmd(p)
}

// threadsFor is the PR's review threads, for panes that show them beside
// the lines they are about. Empty until the comments fetch lands, which
// the file list triggers alongside its own.
func (v *View) threadsFor(p pr) []prThread {
	st, ok := v.comments[p.URL]
	if !ok || st == nil || !st.done {
		return nil
	}
	return st.data.ReviewThreads.Nodes
}

// prByURL finds a loaded PR (own or review section) by its URL.
func (v *View) prByURL(url string) (pr, bool) {
	for _, list := range [][]pr{v.raw, v.reviewRaw} {
		for _, p := range list {
			if p.URL == url {
				return p, true
			}
		}
	}
	return pr{}, false
}

// fetchCommentsCmd fetches one PR's comments; the caller marks the cache
// entry in flight.
func (v *View) fetchCommentsCmd(p pr) tea.Cmd {
	url, num := p.URL, p.Number
	owner, name, _ := strings.Cut(p.repo(), "/")
	return func() tea.Msg {
		out, err := exec.Command("gh", "api", "graphql",
			"-f", "query="+commentsQuery,
			"-f", "owner="+owner,
			"-f", "name="+name,
			"-F", "number="+strconv.Itoa(num),
			"--jq", ".data.repository.pullRequest",
		).Output()
		if err != nil {
			return commentsMsg{url: url, err: cmdErr(err)}
		}
		var data prComments
		if err := json.Unmarshal(out, &data); err != nil {
			return commentsMsg{url: url, err: fmt.Errorf("parsing comments: %w", err)}
		}
		return commentsMsg{url: url, data: data}
	}
}

// threadAnnotations converts inline review threads into diff annotations,
// pinned by right-side line (0 = under the file header for outdated threads).
func threadAnnotations(threads []prThread, width int) []ui.DiffAnnotation {
	anns := make([]ui.DiffAnnotation, 0, len(threads))
	for _, t := range threads {
		line := 0
		if t.Line != nil && !t.IsOutdated {
			line = *t.Line
		}
		anns = append(anns, ui.DiffAnnotation{
			ID:   t.ID,
			Path: t.Path,
			Line: line,
			Text: renderThreadBlock(t, width),
		})
	}
	return anns
}

// renderThreadBlock renders one inline thread as a gutter-barred block.
func renderThreadBlock(t prThread, width int) string {
	bar := ui.Yellow.Render("┃ ")
	if t.IsResolved {
		bar = ui.Dim.Render("┃ ")
	}
	var b []string
	state := ""
	if t.IsResolved {
		state = ui.Green.Render(" ✓ resolved")
	}
	head := ui.Bold.Render("󰆉 "+t.Path+lineSuffix(t)) + state
	b = append(b, bar+ui.Truncate(head, max(1, width-2)))
	for _, c := range t.Comments.Nodes {
		body := firstLine(c.Body)
		line := ui.Cyan.Render("@"+c.Author.Login) + " " + body
		b = append(b, bar+ui.Truncate(line, max(1, width-2)))
	}
	return strings.Join(b, "\n")
}

func lineSuffix(t prThread) string {
	if t.Line == nil {
		return " (outdated)"
	}
	return ":" + strconv.Itoa(*t.Line)
}

// firstLine flattens a comment body to its first non-empty line.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// renderCommentsPane renders the comments pane body: the conversation and
// review verdicts chronologically, then each inline thread in full. It
// returns jump anchors (rendered-line offsets) per thread.
func renderCommentsPane(data prComments, width int) (string, []ui.DiffAnchor) {
	var out []string
	var anchors []ui.DiffAnchor

	type entry struct {
		at     time.Time
		render func()
	}
	var entries []entry
	for _, c := range data.Comments.Nodes {
		c := c
		entries = append(entries, entry{c.CreatedAt, func() {
			out = append(out, commentHeader(c.Author.Login, "", c.CreatedAt))
			out = append(out, strings.Split(ui.Markdown(c.Body, width), "\n")...)
			out = append(out, "")
		}})
	}
	for _, r := range data.Reviews.Nodes {
		r := r
		if strings.TrimSpace(r.Body) == "" && r.State == "COMMENTED" {
			continue // an empty shell review around inline comments
		}
		entries = append(entries, entry{r.CreatedAt, func() {
			out = append(out, commentHeader(r.Author.Login, reviewStateWord(r.State), r.CreatedAt))
			if strings.TrimSpace(r.Body) != "" {
				out = append(out, strings.Split(ui.Markdown(r.Body, width), "\n")...)
			}
			out = append(out, "")
		}})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })

	if len(entries) == 0 {
		out = append(out, ui.Faint.Render("(no conversation)"), "")
	}
	for _, e := range entries {
		e.render()
	}

	if len(data.ReviewThreads.Nodes) > 0 {
		out = append(out, ui.Dim.Render("── inline threads "+strings.Repeat("─", max(0, width-18))))
		for _, t := range data.ReviewThreads.Nodes {
			anchors = append(anchors, ui.DiffAnchor{Line: len(out), ID: t.ID})
			head := ui.Bold.Render("󰆉 " + t.Path + lineSuffix(t))
			if t.IsResolved {
				head += ui.Green.Render("  ✓ resolved")
			}
			out = append(out, head)
			for _, c := range t.Comments.Nodes {
				out = append(out, "  "+ui.Cyan.Render("@"+c.Author.Login)+" "+ui.Dim.Render(ui.Age(c.CreatedAt)))
				for _, l := range strings.Split(ui.Markdown(c.Body, max(20, width-2)), "\n") {
					out = append(out, "  "+l)
				}
			}
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n"), anchors
}

func commentHeader(login, verdict string, at time.Time) string {
	h := ui.Cyan.Render("@" + login)
	if verdict != "" {
		h += " " + verdict
	}
	return h + ui.Dim.Render(" · "+ui.Age(at))
}

func reviewStateWord(state string) string {
	switch state {
	case "APPROVED":
		return ui.Green.Render("approved")
	case "CHANGES_REQUESTED":
		return ui.Red.Render("requested changes")
	case "DISMISSED":
		return ui.Dim.Render("review dismissed")
	default:
		return ui.Dim.Render(strings.ToLower(state))
	}
}

// --- mutations ---------------------------------------------------------------

// threadFlow is an open reply/comment input: replying to an inline thread or
// adding a top-level PR comment.
type threadFlow struct {
	kind       string // "reply" | "comment"
	threadID   string // reply target
	target     string // human label shown in the prompt (path:line, or PR)
	body       string
	submitting bool
}

type threadDoneMsg struct {
	url  string
	what string
	err  error
}

const replyMutation = `mutation($thread: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: { pullRequestReviewThreadId: $thread, body: $body }) {
    comment { id }
  }
}`

const resolveMutation = `mutation($thread: ID!) {
  resolveReviewThread(input: { threadId: $thread }) { thread { id } }
}`

const unresolveMutation = `mutation($thread: ID!) {
  unresolveReviewThread(input: { threadId: $thread }) { thread { id } }
}`

// submitThreadReply posts a reply into an inline review thread.
func submitThreadReply(url, threadID, body string) tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("gh", "api", "graphql",
			"-f", "query="+replyMutation,
			"-f", "thread="+threadID,
			"-f", "body="+body,
		).Run()
		if err != nil {
			return threadDoneMsg{url: url, err: cmdErr(err)}
		}
		return threadDoneMsg{url: url, what: "replied"}
	}
}

// submitTopComment posts a top-level PR comment via gh pr comment.
func submitTopComment(url, repo string, num int, body string) tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("gh", "pr", "comment", strconv.Itoa(num), "-R", repo, "--body", body).Run()
		if err != nil {
			return threadDoneMsg{url: url, err: cmdErr(err)}
		}
		return threadDoneMsg{url: url, what: "commented"}
	}
}

// toggleResolve resolves or unresolves an inline thread.
func toggleResolve(url, threadID string, resolved bool) tea.Cmd {
	mutation := resolveMutation
	what := "resolved thread"
	if resolved {
		mutation = unresolveMutation
		what = "unresolved thread"
	}
	return func() tea.Msg {
		err := exec.Command("gh", "api", "graphql",
			"-f", "query="+mutation,
			"-f", "thread="+threadID,
		).Run()
		if err != nil {
			return threadDoneMsg{url: url, err: cmdErr(err)}
		}
		return threadDoneMsg{url: url, what: what}
	}
}

// currentThread resolves the thread the jump cursor sits on, for reply and
// resolve.
func (v *View) currentThread() (prThread, bool) {
	if (v.pane != paneDiff && v.pane != paneComments) || len(v.anchors) == 0 {
		return prThread{}, false
	}
	idx := v.annIdx
	if idx >= len(v.anchors) {
		idx = 0
	}
	id := v.anchors[idx].ID
	st, ok := v.comments[v.list.Selected().URL]
	if !ok || !st.done {
		return prThread{}, false
	}
	for _, t := range st.data.ReviewThreads.Nodes {
		if t.ID == id {
			return t, true
		}
	}
	return prThread{}, false
}

// updateThreadInput handles keys while a reply/comment prompt is open.
func (v *View) updateThreadInput(msg tea.KeyMsg) tea.Cmd {
	f := v.input
	if f.submitting {
		return nil
	}
	switch msg.String() {
	case "esc":
		v.input = nil
	case "enter":
		if strings.TrimSpace(f.body) == "" {
			return nil
		}
		f.submitting = true
		p := v.list.Selected()
		if f.kind == "reply" {
			return submitThreadReply(p.URL, f.threadID, f.body)
		}
		return submitTopComment(p.URL, p.repo(), p.Number, f.body)
	case "backspace":
		if f.body != "" {
			f.body = f.body[:len(f.body)-1]
		}
	default:
		if kp, ok := tea.Msg(msg).(tea.KeyPressMsg); ok && kp.Text != "" {
			if r := []rune(kp.Text)[0]; r >= 0x20 && r != 0x7f {
				f.body += kp.Text
			}
		}
	}
	return nil
}

// threadPromptLine renders the open reply/comment prompt for the list header.
func (v *View) threadPromptLine() string {
	f := v.input
	if f.submitting {
		return ui.Faint.Render("posting…")
	}
	verb := "reply to "
	if f.kind == "comment" {
		verb = "comment on "
	}
	return ui.Yellow.Render(verb+f.target+": ") + f.body + "█" +
		ui.Faint.Render("  (enter post · esc cancel)")
}
