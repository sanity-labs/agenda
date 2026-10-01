package prs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/ui"
)

// In-pane job logs: enter on a step shows its log in the jobs pane, where it
// scrolls with the list keys and ]/[ jump between error lines; p still pages
// it through less, for searching. gh has no per-step endpoint, so logs are
// fetched per job and cached for the session: a finished job's log never
// changes, and a rerun is a new job id. A job's log is fetched as soon as its
// steps are on screen, which is also how each step knows whether it printed
// anything.

// logLine is one line of a job log: the step it belongs to and its text,
// cleaned of timestamps and anything that could drive the terminal.
type logLine struct {
	step string
	text string
}

// logState is one job's fetched log.
type logState struct {
	lines   []logLine
	perStep map[string]int // lines printed, by step name
	err     error
	done    bool
}

type logMsg struct {
	id    int64
	lines []logLine
	err   error
}

// logView is the log open in the pane.
type logView struct {
	jobID int64
	job   ciJob
	step  ciStep
	// positioned is set once the view has scrolled to its first error, so a
	// repaint does not yank the viewport back there.
	positioned bool
	errIdx     int
	// errs are the rendered lines of the error markers, relative to the log
	// body; head is how many pane lines sit above the body. key/body memoize
	// the render, hidden counts the lines dropped off the top.
	errs   []int
	head   int
	key    string
	body   string
	hidden int
}

// maxLogLines caps what the pane renders of one step. The end of a log is
// where a failure explains itself, so the cap drops lines off the top; p has
// the whole thing.
const maxLogLines = 5000

var (
	// gh defangs the log's colour codes into literal "^[[36m" text.
	defangedSGR = regexp.MustCompile(`\^\[\[[0-9;]*m`)
	// Real escape sequences, should a gh version pass one through: CI
	// output is untrusted and must not reach the terminal as control codes.
	escapeSeq = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b.?")
	logStamp  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z ?`)
)

// cleanLogText makes a log line safe and plain to render: no colour codes,
// escapes or control characters, and tabs as spaces so widths add up.
func cleanLogText(s string) string {
	s = defangedSGR.ReplaceAllString(s, "")
	s = escapeSeq.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\t", "    ")
	s = strings.ReplaceAll(s, "\ufeff", "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// parseLog splits gh's "job<TAB>step<TAB>timestamp text" lines.
func parseLog(out []byte) []logLine {
	raw := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	lines := make([]logLine, 0, len(raw))
	for _, r := range raw {
		parts := strings.SplitN(r, "\t", 3)
		if len(parts) < 3 {
			lines = append(lines, logLine{text: cleanLogText(r)})
			continue
		}
		text := strings.TrimPrefix(parts[2], "\ufeff")
		text = logStamp.ReplaceAllString(text, "")
		lines = append(lines, logLine{step: parts[1], text: cleanLogText(text)})
	}
	return lines
}

func countSteps(lines []logLine) map[string]int {
	n := map[string]int{}
	for _, l := range lines {
		if l.text != "##[endgroup]" {
			n[l.step]++
		}
	}
	return n
}

// fetchLog starts fetching j's log unless it is cached, in flight, or not a
// finished GitHub Actions job (gh has no log for the others).
func (v *View) fetchLog(j ciJob, repo string) tea.Cmd {
	if !j.Actions || j.active() || j.ID == 0 {
		return nil
	}
	if v.logs == nil {
		v.logs = map[int64]*logState{}
	}
	if st, ok := v.logs[j.ID]; ok && (!st.done || st.err == nil) {
		return nil
	}
	v.logs[j.ID] = &logState{}
	id := j.ID
	return func() tea.Msg {
		out, err := ghRetrying("run", "view", "--job", strconv.FormatInt(id, 10), "-R", repo, "--log")
		if err != nil {
			return logMsg{id: id, err: err}
		}
		return logMsg{id: id, lines: parseLog(out)}
	}
}

// maybeFetchLogs fetches the logs of the jobs whose steps are on screen, so
// each step can say whether it has output before you open it.
func (v *View) maybeFetchLogs() tea.Cmd {
	if v.pane != paneJobs {
		return nil
	}
	st, rows, _ := v.jobsCursor()
	if st == nil {
		return nil
	}
	repo := v.list.Selected().repo()
	var cmds []tea.Cmd
	seen := map[int]bool{}
	for _, r := range rows {
		if r.step < 0 || seen[r.job] {
			continue
		}
		seen[r.job] = true
		cmds = append(cmds, v.fetchLog(st.jobs[r.job], repo))
	}
	return tea.Batch(cmds...)
}

func (v *View) applyLog(msg logMsg) tea.Cmd {
	st, ok := v.logs[msg.id]
	if !ok {
		return nil
	}
	st.done, st.err = true, msg.err
	if msg.err == nil {
		st.lines, st.perStep = msg.lines, countSteps(msg.lines)
	}
	if lv := v.logView; lv != nil && lv.jobID == msg.id {
		lv.key = "" // re-render with the data
		v.positionLog()
	}
	return nil
}

// stepLines reports how many lines step s of job j printed, and whether that
// is known yet (its job's log has landed).
func (v *View) stepLines(j ciJob, s ciStep) (int, bool) {
	st, ok := v.logs[j.ID]
	if !ok || !st.done || st.err != nil {
		return 0, false
	}
	return st.perStep[s.Name], true
}

// openLog shows step s of job j in the pane, or says why there is nothing to
// show.
func (v *View) openLog(j ciJob, s ciStep) tea.Cmd {
	switch n, known := v.stepLines(j, s); {
	case !j.Actions:
		return nil
	case j.active():
		// gh refuses the log of a job that is still running.
		v.flash = ui.Yellow.Render(fmt.Sprintf("%s is still running: %s follows it live", j.Name, v.keyHint("open_job")))
		return nil
	case s.bucket() == bucketSkipped:
		v.flash = ui.Dim.Render(s.Name + " was skipped: no output")
		return nil
	case known && n == 0:
		v.flash = ui.Dim.Render(s.Name + " printed nothing")
		return nil
	}
	v.logView = &logView{jobID: j.ID, job: j, step: s}
	cmd := v.fetchLog(j, v.list.Selected().repo())
	v.positionLog()
	return tea.Batch(cmd, ui.RevealPreview)
}

// closeLog returns to the job list, with the cursor where it was.
func (v *View) closeLog() tea.Cmd {
	v.logView = nil
	st, rows, i := v.jobsCursor()
	if i < 0 {
		return nil
	}
	return v.selectRow(st, rows, i)
}

// positionLog scrolls a freshly loaded log to its first error, two thirds of
// the way down the viewport so the lead-up to it shows. A log without errors
// opens at the top.
func (v *View) positionLog() {
	lv := v.logView
	if lv == nil || lv.positioned {
		return
	}
	if st, ok := v.logs[lv.jobID]; !ok || !st.done {
		return
	}
	lv.positioned = true
	v.PreviewView() // lay out lv.errs and lv.head
	if len(lv.errs) > 0 {
		lv.errIdx = 0
		v.jumpLogLine(lv.errs[0])
	}
}

// jumpLogLine asks the root to scroll body line i into view.
func (v *View) jumpLogLine(i int) {
	top := v.paneHeader + v.logView.head + i - (2*v.height)/3
	line := max(0, top) + 1 // the root scrolls to line-1
	v.pendingJump = &line
}

// jumpError moves between error lines, wrapping.
func (v *View) jumpError(d int) tea.Cmd {
	lv := v.logView
	if len(lv.errs) == 0 {
		v.flash = ui.Dim.Render("no errors marked in this log")
		return nil
	}
	n := len(lv.errs)
	lv.errIdx = ((lv.errIdx+d)%n + n) % n
	v.jumpLogLine(lv.errs[lv.errIdx])
	return nil
}

// updateLogKeys handles a key while a log is open in the pane. The movement
// keys scroll it; what it leaves goes on to the jobs pane's handling.
func (v *View) updateLogKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch {
	case msg.String() == "esc", msg.String() == "left":
		return v.closeLog(), true
	case msg.String() == "enter":
		return nil, true
	case key.Matches(msg, v.nav.Up):
		v.scrollBy--
	case key.Matches(msg, v.nav.Down):
		v.scrollBy++
	case key.Matches(msg, v.nav.HalfUp):
		v.scrollBy -= max(1, v.height/2)
	case key.Matches(msg, v.nav.HalfDown):
		v.scrollBy += max(1, v.height/2)
	case key.Matches(msg, v.nav.Top):
		line := 1
		v.pendingJump = &line
	case key.Matches(msg, v.nav.Bottom):
		line := 1 << 30 // clamped to the end
		v.pendingJump = &line
	case key.Matches(msg, v.keys.NextThread):
		return v.jumpError(1), true
	case key.Matches(msg, v.keys.PrevThread):
		return v.jumpError(-1), true
	case key.Matches(msg, v.keys.Copy):
		return v.copyJobURL(), true
	default:
		return nil, false
	}
	return nil, true
}

// TakePreviewScroll implements the root model's relative-scroll hook: j/k
// through an open log.
func (v *View) TakePreviewScroll() (int, bool) {
	d := v.scrollBy
	v.scrollBy = 0
	return d, d != 0
}

// logKey keeps the preview key distinct per open log, so opening one starts
// at its top and closing it returns to the list's own scroll.
func (v *View) logKey() string {
	if lv := v.logView; lv != nil {
		return fmt.Sprintf("#log:%d:%d", lv.jobID, lv.step.Number)
	}
	return ""
}

// logBindings is the footer while a log is open.
func (v *View) logBindings() []key.Binding {
	help := func(k, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc))
	}
	return []key.Binding{
		help("esc", "back to jobs"),
		help(v.keyHint("next_thread")+"/"+v.keyHint("prev_thread"), "errors"),
		help(v.keyHint("job_log"), "pager"),
		help(v.keyHint("open_job"), "open"),
		help(ui.HelpLabel(firstKey(v.nav.Down))+"/"+ui.HelpLabel(firstKey(v.nav.Up)), "scroll"),
	}
}

// renderedLog renders the open log: a heading naming the job and step, then
// the step's lines.
func (v *View) renderedLog() string {
	lv := v.logView
	var head []string
	head = append(head, jobGlyph(lv.step.bucket())+" "+ui.Bold.Render(lv.job.Name)+
		ui.Dim.Render(" › ")+ui.Bold.Render(lv.step.Name))

	st, ok := v.logs[lv.jobID]
	switch {
	case !ok || !st.done:
		lv.head = len(head)
		return strings.Join(append(head, "", ui.Faint.Render("Loading log…")), "\n")
	case st.err != nil:
		lv.head = len(head)
		return strings.Join(append(head, "", ui.Red.Render(st.err.Error())), "\n")
	}

	key := fmt.Sprintf("%d:%d:%d:%d", lv.jobID, lv.step.Number, v.prevW, ui.PaletteGen())
	if lv.key != key {
		lv.body, lv.errs, lv.hidden = renderLogBody(st.lines, lv.step.Name, v.prevW)
		lv.key = key
	}

	meta := []string{fmt.Sprintf("%d lines", st.perStep[lv.step.Name])}
	if d := stepDuration(lv.step, time.Now()); d != "" {
		meta = append(meta, d)
	}
	switch n := len(lv.errs); n {
	case 0:
	case 1:
		meta = append(meta, ui.Red.Render("1 error"))
	default:
		meta = append(meta, ui.Red.Render(fmt.Sprintf("%d errors", n)))
	}
	head = append(head, ui.Dim.Render(strings.Join(meta, " · ")),
		ui.Faint.Render(fmt.Sprintf("esc back · %s/%s errors · %s pager · %s open",
			v.keyHint("next_thread"), v.keyHint("prev_thread"), v.keyHint("job_log"), v.keyHint("open_job"))),
		"")
	if lv.hidden > 0 {
		head = append(head, ui.Faint.Render(fmt.Sprintf("… %d earlier lines · %s for the whole log",
			lv.hidden, v.keyHint("job_log"))))
	}
	lv.head = len(head)
	if lv.body == "" {
		return strings.Join(append(head, ui.Faint.Render("(no output)")), "\n")
	}
	return strings.Join(head, "\n") + "\n" + lv.body
}

// renderLogBody renders one step's lines, wrapped to width, styled by the
// workflow-command markers GitHub puts in logs. errs are the rendered lines of
// the ##[error] markers; hidden counts lines dropped by the cap.
func renderLogBody(lines []logLine, step string, width int) (string, []int, int) {
	var src []logLine
	for _, l := range lines {
		if l.step == step {
			src = append(src, l)
		}
	}
	hidden := 0
	if len(src) > maxLogLines {
		hidden = len(src) - maxLogLines
		src = src[hidden:]
	}
	w := max(20, width-1)
	var out []string
	var errs []int
	for _, l := range src {
		text, style, isErr, skip := classifyLogLine(l.text)
		if skip {
			continue
		}
		if isErr {
			errs = append(errs, len(out))
		}
		for _, seg := range strings.Split(ansi.Hardwrap(text, w, true), "\n") {
			out = append(out, style.Render(seg))
		}
	}
	return strings.Join(out, "\n"), errs, hidden
}

// classifyLogLine reads GitHub's workflow-command markers: groups become a
// dim heading (their end marker disappears), errors and warnings take their
// colour, and commands read like a shell prompt.
func classifyLogLine(t string) (text string, style lipgloss.Style, isErr, skip bool) {
	plain := lipgloss.NewStyle()
	cut := func(prefix string) (string, bool) { return strings.CutPrefix(t, prefix) }
	if rest, ok := cut("##[group]"); ok {
		return "▸ " + rest, ui.Dim, false, false
	}
	if t == "##[endgroup]" {
		return "", plain, false, true
	}
	if rest, ok := cut("##[error]"); ok {
		return ui.IconCIFail + " " + rest, ui.Red, true, false
	}
	if rest, ok := cut("##[warning]"); ok {
		return "! " + rest, ui.Yellow, false, false
	}
	if rest, ok := cut("##[notice]"); ok {
		return rest, ui.Cyan, false, false
	}
	if rest, ok := cut("##[debug]"); ok {
		return rest, ui.Faint, false, false
	}
	if rest, ok := cut("##[command]"); ok {
		return "$ " + rest, ui.Cyan, false, false
	}
	if rest, ok := cut("[command]"); ok {
		return "$ " + rest, ui.Cyan, false, false
	}
	// Markers this does not know (##[start-action …], ##[end-action …])
	// are runner bookkeeping: keep them, but out of the way.
	if strings.HasPrefix(t, "##[") {
		return t, ui.Faint, false, false
	}
	return t, plain, false, false
}
