package prs

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// PR jobs: the check runs and commit statuses on a PR's head commit, grouped
// by workflow run, with the failed steps of each failed job. Fetched when the
// jobs pane opens ('t'), cached per PR URL, and refetched on a timer while
// anything is still running. From the pane a job's log pages through less and
// GitHub Actions jobs can be rerun. The idea is gh-enhance's (dlvhdr); the
// code is agenda's own.

// jobBucket orders jobs worst-first: what needs attention floats to the top of
// its group, and the group with the worst job floats to the top of the pane.
type jobBucket int

const (
	bucketFail jobBucket = iota
	bucketCancelled
	bucketRunning
	bucketQueued
	bucketSkipped
	bucketPass
	// bucketUnreported is a step whose state GitHub never settled; only
	// steps land here, and nothing sorts on it.
	bucketUnreported
)

type ciStep struct {
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	// Unreported marks a step GitHub still calls pending or in progress
	// although its job has finished: the step data lags the job, sometimes
	// for good. It is neither running nor anything else we can name.
	Unreported bool `json:"-"`
}

// ciJob is one row of the pane: a check run, or a commit status.
type ciJob struct {
	// ID is the check run's database id, which for GitHub Actions is also
	// the job id gh takes. 0 for a commit status.
	ID   int64
	Name string
	// Group is the heading the job is listed under (workflow, app, or
	// "Statuses"); groupKey is what actually separates groups, since one
	// workflow can run twice on a commit (push and pull_request).
	Group    string
	groupKey string
	// RunID is the workflow run, 0 outside GitHub Actions. Actions marks
	// the jobs whose logs gh can fetch and whose runs gh can rerun.
	RunID       int64
	RunNumber   int
	Event       string
	Actions     bool
	Status      string
	Conclusion  string
	Description string
	Required    bool
	URL         string
	StartedAt   time.Time
	CompletedAt time.Time
	Steps       []ciStep
}

func (j ciJob) bucket() jobBucket {
	return bucketOf(j.Status, j.Conclusion)
}

func (s ciStep) bucket() jobBucket {
	if s.Unreported {
		return bucketUnreported
	}
	return bucketOf(s.Status, s.Conclusion)
}

func bucketOf(status, conclusion string) jobBucket {
	switch status {
	case "IN_PROGRESS":
		return bucketRunning
	case "QUEUED", "PENDING", "WAITING", "REQUESTED", "EXPECTED":
		return bucketQueued
	}
	switch conclusion {
	case "SUCCESS":
		return bucketPass
	case "SKIPPED", "NEUTRAL", "STALE":
		return bucketSkipped
	case "CANCELLED":
		return bucketCancelled
	case "":
		// Completed with no conclusion yet: GitHub is still settling it.
		return bucketQueued
	}
	return bucketFail // FAILURE, TIMED_OUT, STARTUP_FAILURE, ACTION_REQUIRED, ERROR
}

// active is a job that has not finished: running, queued, or waiting.
func (j ciJob) active() bool {
	b := j.bucket()
	return b == bucketRunning || b == bucketQueued
}

// jobsState tracks one PR's jobs. Data from an earlier fetch stays on screen
// while a refetch is in flight, so the watch timer never blanks the pane.
type jobsState struct {
	jobs      []ciJob // sorted, see sortJobs
	sha       string
	err       error
	done      bool // a fetch has landed at least once
	inFlight  bool
	fetchedAt time.Time
	// rerunAt keeps the watch going for a while after a rerun, since the
	// new attempt takes a few seconds to show up as queued.
	rerunAt time.Time
	// failures counts consecutive failed fetches, bounding the retries.
	failures int
}

func (s *jobsState) activeCount() int {
	n := 0
	for _, j := range s.jobs {
		if j.active() {
			n++
		}
	}
	return n
}

// jobsWatchEvery is how often a pane with unfinished jobs refetches. The
// rollup changes on GitHub's clock, and ten seconds keeps a running job's
// state current without hammering the API.
const jobsWatchEvery = 10 * time.Second

// rerunGrace is how long a pane keeps watching after a rerun even when
// nothing reads as running yet.
const rerunGrace = time.Minute

// jobsRetries and jobsRetryAfter bound the automatic retry of a failed
// fetch: GitHub's GraphQL gateway fails transiently, and a pane stuck on an
// error until you toggle it is worse than two quiet retries.
const (
	jobsRetries    = 2
	jobsRetryAfter = 5 * time.Second
)

// stale reports whether the pane should refetch on (re)entry: soon while
// anything runs, rarely once everything has settled.
func (s *jobsState) stale(now time.Time) bool {
	if !s.done || s.fetchedAt.IsZero() || s.err != nil {
		return true
	}
	ttl := 2 * time.Minute
	if s.watching(now) {
		ttl = jobsWatchEvery - time.Second
	}
	return now.Sub(s.fetchedAt) >= ttl
}

// watching reports whether the pane should keep polling.
func (s *jobsState) watching(now time.Time) bool {
	return s.activeCount() > 0 || (!s.rerunAt.IsZero() && now.Sub(s.rerunAt) < rerunGrace)
}

type jobsMsg struct {
	url  string
	jobs []ciJob
	sha  string
	err  error
}

// jobsTickMsg drives the watch; only the newest generation acts.
type jobsTickMsg struct {
	from *View
	url  string
	gen  int
}

// jobsQuery reads the head commit's checks. Steps come along for every check
// run: they only exist for GitHub Actions, and fetching them here keeps the
// pane to one request instead of one per job.
const jobsQuery = `query($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      commits(last: 1) { nodes { commit { oid statusCheckRollup {
        contexts(first: 100, after: $after) {
          pageInfo { hasNextPage endCursor }
          nodes {
            __typename
            ... on CheckRun {
              databaseId name status conclusion startedAt completedAt detailsUrl
              isRequired(pullRequestNumber: $number)
              checkSuite { app { name } workflowRun { databaseId runNumber event workflow { name } } }
              steps(first: 100) { nodes { number name status conclusion startedAt completedAt } }
            }
            ... on StatusContext {
              context state description targetUrl createdAt
              isRequired(pullRequestNumber: $number)
            }
          }
        }
      } } } }
    }
  }
}`

// jobsMaxPages caps the contexts pagination. 500 checks on one commit is
// already far past anything a pane can usefully show.
const jobsMaxPages = 5

// rawContext is one statusCheckRollup context: a CheckRun or a StatusContext,
// decoded into one struct since the two share no conflicting field names.
type rawContext struct {
	Typename string `json:"__typename"`

	DatabaseID  int64     `json:"databaseId"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	DetailsURL  string    `json:"detailsUrl"`
	IsRequired  bool      `json:"isRequired"`
	CheckSuite  struct {
		App struct {
			Name string `json:"name"`
		} `json:"app"`
		WorkflowRun *struct {
			DatabaseID int64  `json:"databaseId"`
			RunNumber  int    `json:"runNumber"`
			Event      string `json:"event"`
			Workflow   struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
	Steps struct {
		Nodes []ciStep `json:"nodes"`
	} `json:"steps"`

	Context     string    `json:"context"`
	State       string    `json:"state"`
	Description string    `json:"description"`
	TargetURL   string    `json:"targetUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

type rawCommit struct {
	OID               string `json:"oid"`
	StatusCheckRollup struct {
		Contexts struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []rawContext `json:"nodes"`
		} `json:"contexts"`
	} `json:"statusCheckRollup"`
}

// ghOutput and ghCombined run gh, returning stdout or stdout+stderr. Package
// variables so tests can stand in for gh.
var (
	ghOutput = func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "gh", args...).Output()
	}
	ghCombined = func(args ...string) ([]byte, error) {
		return exec.Command("gh", args...).CombinedOutput()
	}
	// ghBackoff waits between retries; tests make it instant.
	ghBackoff = time.Sleep
)

// ghTransient reports an error GitHub is likely to answer differently a few
// seconds later: its gateway's HTML 5xx page (which gh then fails to parse,
// hence "invalid character '<'"), an explicit 502/503/504, or a timeout. Not
// a rate limit: GitHub reports those as JSON, and retrying makes them worse.
func ghTransient(err error) bool {
	msg := err.Error()
	for _, s := range []string{"invalid character '<'", "HTTP 502", "HTTP 503", "HTTP 504",
		"i/o timeout", "context deadline exceeded"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// ghRetrying runs gh, retrying a transient failure twice with a short
// backoff before reporting it in words rather than as a JSON parse error.
func ghRetrying(args ...string) ([]byte, error) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			ghBackoff(time.Duration(attempt) * time.Second)
		}
		var out []byte
		if out, err = ghOutput(args...); err == nil {
			return out, nil
		}
		if err = cmdErr(err); !ghTransient(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("GitHub returned an error page (a transient 5xx): %w", err)
}

// maybeFetchJobs starts a jobs fetch for the selected PR when the jobs pane is
// showing and what is cached is missing or stale.
func (v *View) maybeFetchJobs() tea.Cmd {
	if v.pane != paneJobs {
		return nil
	}
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	if v.jobs == nil {
		v.jobs = map[string]*jobsState{}
	}
	st, ok := v.jobs[p.URL]
	if !ok {
		st = &jobsState{}
		v.jobs[p.URL] = st
	}
	if st.inFlight || !st.stale(time.Now()) {
		return nil
	}
	st.inFlight = true
	return fetchJobsCmd(p.URL, p.repo(), p.Number)
}

// fetchJobsCmd reads every page of the PR's head-commit checks.
func fetchJobsCmd(url, repo string, num int) tea.Cmd {
	owner, name, _ := strings.Cut(repo, "/")
	return func() tea.Msg {
		var nodes []rawContext
		var sha, after string
		for page := 0; page < jobsMaxPages; page++ {
			args := []string{"api", "graphql",
				"-f", "query=" + jobsQuery,
				"-f", "owner=" + owner,
				"-f", "name=" + name,
				"-F", "number=" + strconv.Itoa(num),
				"--jq", ".data.repository.pullRequest.commits.nodes[0].commit",
			}
			if after != "" {
				args = append(args, "-f", "after="+after)
			}
			out, err := ghRetrying(args...)
			if err != nil {
				return jobsMsg{url: url, err: err}
			}
			var c rawCommit
			if err := json.Unmarshal(out, &c); err != nil {
				return jobsMsg{url: url, err: fmt.Errorf("parsing checks: %w", err)}
			}
			sha = c.OID
			cx := c.StatusCheckRollup.Contexts
			nodes = append(nodes, cx.Nodes...)
			if !cx.PageInfo.HasNextPage {
				break
			}
			after = cx.PageInfo.EndCursor
		}
		return jobsMsg{url: url, jobs: toJobs(nodes), sha: sha}
	}
}

// toJobs turns rollup contexts into pane rows: deduplicated, grouped and
// sorted worst-first.
func toJobs(nodes []rawContext) []ciJob {
	byKey := map[string]int{}
	var jobs []ciJob
	keep := func(key string, j ciJob) {
		if i, seen := byKey[key]; seen {
			// A rerun adds a new check run under the same name; the newer
			// attempt has the larger id and is the one that counts.
			if j.ID > jobs[i].ID {
				jobs[i] = j
			}
			return
		}
		byKey[key] = len(jobs)
		jobs = append(jobs, j)
	}
	for _, n := range nodes {
		switch n.Typename {
		case "CheckRun":
			j := ciJob{
				ID:          n.DatabaseID,
				Name:        n.Name,
				Status:      n.Status,
				Conclusion:  n.Conclusion,
				Required:    n.IsRequired,
				URL:         n.DetailsURL,
				StartedAt:   n.StartedAt,
				CompletedAt: n.CompletedAt,
				Steps:       n.Steps.Nodes,
			}
			if j.Status == "COMPLETED" {
				j.Steps = settleSteps(j.Steps)
			}
			if wr := n.CheckSuite.WorkflowRun; wr != nil {
				j.Actions = true
				j.RunID, j.RunNumber, j.Event = wr.DatabaseID, wr.RunNumber, wr.Event
				j.Group = wr.Workflow.Name
				j.groupKey = "run:" + strconv.FormatInt(wr.DatabaseID, 10)
			} else {
				j.Group = n.CheckSuite.App.Name
				if j.Group == "" {
					j.Group = "Checks"
				}
				j.groupKey = "app:" + j.Group
			}
			keep(j.groupKey+"/"+j.Name, j)
		case "StatusContext":
			j := ciJob{
				Name:        n.Context,
				Group:       "Statuses",
				groupKey:    "status",
				Description: n.Description,
				Required:    n.IsRequired,
				URL:         n.TargetURL,
				StartedAt:   n.CreatedAt,
			}
			switch n.State {
			case "PENDING", "EXPECTED":
				j.Status = "PENDING"
			default:
				j.Status, j.Conclusion = "COMPLETED", n.State
			}
			keep("status/"+j.Name, j)
		}
	}
	labelRuns(jobs)
	sortJobs(jobs)
	return jobs
}

// settleSteps marks the steps of a finished job that GitHub has not finished
// reporting, so they do not read as running forever.
func settleSteps(steps []ciStep) []ciStep {
	out := slices.Clone(steps)
	for i := range out {
		if out[i].Status != "COMPLETED" {
			out[i].Unreported = true
		}
	}
	return out
}

// labelRuns disambiguates a workflow that ran more than once on the commit
// (typically on both push and pull_request) by appending the event.
func labelRuns(jobs []ciJob) {
	runs := map[string]map[string]bool{} // workflow name -> group keys
	for _, j := range jobs {
		if !j.Actions {
			continue
		}
		if runs[j.Group] == nil {
			runs[j.Group] = map[string]bool{}
		}
		runs[j.Group][j.groupKey] = true
	}
	for i := range jobs {
		if jobs[i].Actions && len(runs[jobs[i].Group]) > 1 && jobs[i].Event != "" {
			jobs[i].Group += " · " + jobs[i].Event
		}
	}
}

// sortJobs orders groups by their worst job, then name; and jobs within a
// group worst-first, then by name.
func sortJobs(jobs []ciJob) {
	worst := map[string]jobBucket{}
	for _, j := range jobs {
		if b, ok := worst[j.groupKey]; !ok || j.bucket() < b {
			worst[j.groupKey] = j.bucket()
		}
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		ja, jb := jobs[a], jobs[b]
		if ja.groupKey != jb.groupKey {
			if wa, wb := worst[ja.groupKey], worst[jb.groupKey]; wa != wb {
				return wa < wb
			}
			if ja.Group != jb.Group {
				return strings.ToLower(ja.Group) < strings.ToLower(jb.Group)
			}
			return ja.groupKey < jb.groupKey
		}
		if ba, bb := ja.bucket(), jb.bucket(); ba != bb {
			return ba < bb
		}
		return strings.ToLower(ja.Name) < strings.ToLower(jb.Name)
	})
}

// applyJobs stores a fetch and decides what follows it: another watch tick
// while anything runs, and a re-read of the row once everything has settled,
// since the list's rollup state is then out of date.
func (v *View) applyJobs(msg jobsMsg) tea.Cmd {
	st, ok := v.jobs[msg.url]
	if !ok {
		return nil
	}
	now := time.Now()
	wasActive := st.done && st.activeCount() > 0
	st.inFlight = false
	st.fetchedAt = now
	st.err = msg.err
	st.done = true
	if msg.err == nil {
		st.jobs, st.sha = msg.jobs, msg.sha
		st.failures = 0
	} else {
		st.failures++
	}

	var cmds []tea.Cmd
	viewing := v.pane == paneJobs && v.list.Selected().URL == msg.url
	switch {
	case viewing && msg.err != nil && st.failures <= jobsRetries:
		cmds = append(cmds, v.scheduleJobsWatch(msg.url, jobsRetryAfter))
	case viewing && st.watching(now):
		cmds = append(cmds, v.scheduleJobsWatch(msg.url, jobsWatchEvery))
	}
	if msg.err == nil && wasActive && st.activeCount() == 0 {
		cmds = append(cmds, v.refreshRow(msg.url))
	}
	if viewing {
		cmds = append(cmds, v.maybeFetchLogs())
	}
	return tea.Batch(cmds...)
}

// scheduleJobsWatch arms the next watch tick, superseding any pending one.
func (v *View) scheduleJobsWatch(url string, after time.Duration) tea.Cmd {
	v.jobsGen++
	gen := v.jobsGen
	return tea.Tick(after, func(time.Time) tea.Msg { return jobsTickMsg{from: v, url: url, gen: gen} })
}

// selectedJobs is the jobs state for the selected PR, nil when none is loaded.
func (v *View) selectedJobs() *jobsState {
	st, ok := v.jobs[v.list.Selected().URL]
	if !ok || !st.done {
		return nil
	}
	return st
}

// --- cursor and navigation --------------------------------------------------

// The pane is a list of rows: every job, and under each job the steps worth
// seeing without asking (the failed or running one), or all of its steps once
// expanded. The cursor is kept by identity rather than index, so a watch
// refetch that re-sorts the jobs leaves it on the job it was on.

// jobRow is one selectable line of the pane.
type jobRow struct {
	job  int // index into jobsState.jobs
	step int // index into that job's Steps; -1 for the job's own row
}

// jobCursor names the selected row: a job by key, and a step by its number
// (0 for the job row; GitHub numbers steps from 1).
type jobCursor struct {
	job  string
	step int
}

// key identifies a job across refetches: its group (the workflow run, which
// a rerun keeps) and name, the same pair toJobs deduplicates on.
func (j ciJob) key() string { return j.groupKey + "/" + j.Name }

func (r jobRow) cursor(jobs []ciJob) jobCursor {
	c := jobCursor{job: jobs[r.job].key()}
	if r.step >= 0 {
		c.step = jobs[r.job].Steps[r.step].Number
	}
	return c
}

// shownSteps are the indices of the steps listed under j: all of them when
// expanded, otherwise the ones that explain its state.
func shownSteps(j ciJob, expanded bool) []int {
	want := bucketFail
	switch b := j.bucket(); {
	case expanded:
	case b == bucketFail || b == bucketCancelled:
	case b == bucketRunning:
		want = bucketRunning
	default:
		return nil
	}
	var out []int
	for i, s := range j.Steps {
		if expanded || s.bucket() == want {
			out = append(out, i)
		}
	}
	return out
}

func jobRows(jobs []ciJob, open map[string]bool) []jobRow {
	rows := make([]jobRow, 0, len(jobs))
	for i, j := range jobs {
		rows = append(rows, jobRow{job: i, step: -1})
		for _, si := range shownSteps(j, open[j.key()]) {
			rows = append(rows, jobRow{job: i, step: si})
		}
	}
	return rows
}

// jobsCursor returns the selected PR's jobs, the pane's rows, and the index
// of the row under the cursor (-1 with nothing loaded). Cursor state belongs
// to one PR: arriving at another starts it over at the top.
func (v *View) jobsCursor() (*jobsState, []jobRow, int) {
	if url := v.list.Selected().URL; v.jobsFor != url {
		v.jobsFor, v.jobSel, v.jobsOpen, v.logView = url, jobCursor{}, nil, nil
	}
	if v.pane != paneJobs {
		v.jobsFocus, v.logView = false, nil
	}
	st := v.selectedJobs()
	if st == nil || len(st.jobs) == 0 {
		return st, nil, -1
	}
	rows := jobRows(st.jobs, v.jobsOpen)
	parent := -1
	for i, r := range rows {
		c := r.cursor(st.jobs)
		if c == v.jobSel {
			return st, rows, i
		}
		if r.step < 0 && c.job == v.jobSel.job {
			parent = i
		}
	}
	// The row is gone (a step of a job since collapsed, or one no longer
	// listed): fall back to its job, else the top.
	i := max(parent, 0)
	v.jobSel = rows[i].cursor(st.jobs)
	return st, rows, i
}

// currentRow is the job under the cursor, and the step when the cursor is on
// one.
func (v *View) currentRow() (ciJob, *ciStep, bool) {
	if v.pane != paneJobs {
		return ciJob{}, nil, false
	}
	st, rows, i := v.jobsCursor()
	if i < 0 {
		return ciJob{}, nil, false
	}
	j := st.jobs[rows[i].job]
	if rows[i].step >= 0 {
		return j, &j.Steps[rows[i].step], true
	}
	return j, nil, true
}

// currentJob is the job under the cursor, or the step's job.
func (v *View) currentJob() (ciJob, bool) {
	j, _, ok := v.currentRow()
	return j, ok
}

// selectRow puts the cursor on row i and scrolls to keep it a third of the
// way down the viewport, so the summary stays in view until the cursor leaves
// the first screen.
func (v *View) selectRow(st *jobsState, rows []jobRow, i int) tea.Cmd {
	i = max(0, min(i, len(rows)-1))
	v.jobSel = rows[i].cursor(st.jobs)
	v.PreviewView() // lay out the anchors for the row just selected
	if i < len(v.anchors) {
		line := v.anchors[i].Line + v.paneHeader - v.height/3
		v.pendingJump = &line
	}
	return ui.RevealPreview
}

// moveJob moves the cursor by d rows, stopping at either end.
func (v *View) moveJob(d int) tea.Cmd {
	st, rows, i := v.jobsCursor()
	if i < 0 {
		return nil
	}
	return v.selectRow(st, rows, min(max(i+d, 0), len(rows)-1))
}

// jumpFailed moves to the next (d=1) or previous (d=-1) failed job, wrapping;
// with nothing failed it walks the jobs instead. It also focuses the pane,
// since the cursor it moves is the pane's.
func (v *View) jumpFailed(d int) tea.Cmd {
	st, rows, i := v.jobsCursor()
	if i < 0 {
		return nil
	}
	v.jobsFocus = true
	target := func(r jobRow) bool {
		b := st.jobs[r.job].bucket()
		return r.step < 0 && (b == bucketFail || b == bucketCancelled)
	}
	if !slices.ContainsFunc(rows, target) {
		target = func(r jobRow) bool { return r.step < 0 }
	}
	n := len(rows)
	for k := 1; k <= n; k++ {
		if j := ((i+d*k)%n + n) % n; target(rows[j]) {
			return v.selectRow(st, rows, j)
		}
	}
	return nil
}

// toggleOpen expands or collapses the job at row i. Collapsing from one of
// its steps moves the cursor up to the job, so it is not left on a row that
// disappears.
func (v *View) toggleOpen(st *jobsState, rows []jobRow, i int) tea.Cmd {
	j := st.jobs[rows[i].job]
	if len(j.Steps) == 0 {
		return nil
	}
	if v.jobsOpen == nil {
		v.jobsOpen = map[string]bool{}
	}
	v.jobsOpen[j.key()] = !v.jobsOpen[j.key()]
	v.jobSel = jobCursor{job: j.key()}
	st, rows, i = v.jobsCursor()
	// Opened, its steps are on screen: fetch the log that says which of
	// them printed anything.
	return tea.Batch(v.selectRow(st, rows, i), v.maybeFetchLogs())
}

// activateRow is enter on a row: a job with steps opens or closes, a step
// shows its log in the pane, and a check with neither opens in the browser.
func (v *View) activateRow() tea.Cmd {
	st, rows, i := v.jobsCursor()
	if i < 0 {
		return nil
	}
	j := st.jobs[rows[i].job]
	switch {
	case rows[i].step >= 0:
		return v.openLog(j, j.Steps[rows[i].step])
	case len(j.Steps) > 0:
		return v.toggleOpen(st, rows, i)
	default:
		return v.openJob()
	}
}

// updateJobsFocus handles a key while the jobs pane has focus. It reports
// whether it took the key; anything it leaves goes on to the view's usual
// handling, so the PR-level keys keep working from the pane.
func (v *View) updateJobsFocus(msg tea.KeyMsg) (tea.Cmd, bool) {
	st, rows, i := v.jobsCursor()
	switch {
	case msg.String() == "esc":
		v.jobsFocus = false
		return nil, true
	case msg.String() == "enter":
		return v.activateRow(), true
	case msg.String() == "right":
		if i >= 0 && rows[i].step < 0 && !v.jobsOpen[st.jobs[rows[i].job].key()] {
			return v.toggleOpen(st, rows, i), true
		}
		return v.moveJob(1), true
	case msg.String() == "left":
		switch {
		case i < 0:
			v.jobsFocus = false
		case rows[i].step >= 0:
			// Up to the step's job, as a tree does.
			v.jobSel = jobCursor{job: st.jobs[rows[i].job].key()}
			st, rows, i = v.jobsCursor()
			return v.selectRow(st, rows, i), true
		case v.jobsOpen[st.jobs[rows[i].job].key()]:
			return v.toggleOpen(st, rows, i), true
		default:
			v.jobsFocus = false
		}
		if !v.jobsFocus && v.floatReveal {
			return v.leaveFloat(), true
		}
		return nil, true
	case key.Matches(msg, v.nav.Up):
		return v.moveJob(-1), true
	case key.Matches(msg, v.nav.Down):
		return v.moveJob(1), true
	case key.Matches(msg, v.nav.JumpUp):
		return v.moveJob(-v.jumpSize()), true
	case key.Matches(msg, v.nav.JumpDown):
		return v.moveJob(v.jumpSize()), true
	case key.Matches(msg, v.nav.Top):
		return v.moveJob(-len(rows)), true
	case key.Matches(msg, v.nav.Bottom):
		return v.moveJob(len(rows)), true
	case key.Matches(msg, v.nav.HalfUp):
		return v.moveJob(-max(1, v.height/2)), true
	case key.Matches(msg, v.nav.HalfDown):
		return v.moveJob(max(1, v.height/2)), true
	case key.Matches(msg, v.keys.Copy):
		return v.copyJobURL(), true
	}
	return nil, false
}

// clickJobRow handles a click on preview line: a row takes the cursor and
// focus, and a click on the row already selected expands or collapses a job.
// ok is false when the line is not a row.
func (v *View) clickJobRow(line int) (tea.Cmd, bool) {
	st, rows, sel := v.jobsCursor()
	for i, a := range v.anchors {
		if a.Line+v.paneHeader != line || i >= len(rows) {
			continue
		}
		wasFocused := v.jobsFocus
		v.jobsFocus = true
		if i == sel && wasFocused && rows[i].step < 0 {
			return v.toggleOpen(st, rows, i), true
		}
		v.jobSel = rows[i].cursor(st.jobs)
		return nil, true
	}
	return nil, false
}

// navKeys are the list's movement keys (keys.list.*), which the jobs pane
// honours too so a remap applies in both.
type navKeys struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, JumpUp, JumpDown key.Binding
}

func newNavKeys(km config.Keymap) navKeys {
	bind := func(action string, def ...string) key.Binding {
		return ui.Bind(km.Of("list", action, def...), "", "")
	}
	return navKeys{
		Up:       bind("up", "up", "k"),
		Down:     bind("down", "down", "j"),
		Top:      bind("top", "g", "home"),
		Bottom:   bind("bottom", "G", "end"),
		HalfUp:   bind("half_up", "ctrl+u"),
		HalfDown: bind("half_down", "ctrl+d"),
		JumpUp:   bind("jump_up", "shift+up", "pgup"),
		JumpDown: bind("jump_down", "shift+down", "pgdown"),
	}
}

// jobURL is where the row under the cursor lives on the web: the job page,
// scrolled to the step for a step of a GitHub Actions job.
func (v *View) jobURL() string {
	j, s, ok := v.currentRow()
	if !ok {
		return ""
	}
	if s != nil && j.Actions && j.URL != "" {
		return fmt.Sprintf("%s#step:%d:1", j.URL, s.Number)
	}
	return j.URL
}

// openJob opens the row under the cursor on GitHub (or wherever an external
// check links to).
func (v *View) openJob() tea.Cmd {
	return ui.OpenURL(v.jobURL())
}

func (v *View) copyJobURL() tea.Cmd {
	u := v.jobURL()
	if u == "" {
		return nil
	}
	v.flash = ui.Green.Render("✓ copied job URL")
	return func() tea.Msg {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(u)
		_ = c.Run()
		return nil
	}
}

// logScript pages one job's log. Arguments are positional ($1 job id, $2
// repo, $3 the gh log flag, $4 a step name to narrow to, $5 less's start
// position) so nothing from the API is ever parsed by the shell. gh defangs
// the log's colour codes into literal "^[[36m" text; they are stripped rather
// than turned back into escapes a CI log controls. The step filter matches
// gh's step column, and a step with no lines of its own shows the whole job
// rather than an empty pager. cut drops the job-name column, the same on
// every line.
const logScript = `log=$(gh run view --job "$1" -R "$2" "$3" 2>&1 | sed -E 's/\^\[\[[0-9;]*m//g')
if [ -n "$4" ]; then
  only=$(printf '%s\n' "$log" | S="$4" awk -F '\t' '$2 == ENVIRON["S"]')
  [ -n "$only" ] && log=$only
fi
printf '%s\n' "$log" | cut -f2- | less $5`

// jobLogInPager pages the log of the row under the cursor through less: a
// step's own lines, or a whole job. A failed job shows only its failed steps;
// anything failed opens at the end, where the error usually is.
func (v *View) jobLogInPager() tea.Cmd {
	j, s, ok := v.currentRow()
	if !ok {
		return nil
	}
	switch {
	case !j.Actions:
		v.flash = ui.Yellow.Render(fmt.Sprintf("no log for %s here: %s opens it", j.Name, v.keyHint("open_job")))
		return nil
	case j.active():
		// gh refuses the log of a job that is still running.
		v.flash = ui.Yellow.Render(fmt.Sprintf("%s is still running: %s follows it live", j.Name, v.keyHint("open_job")))
		return nil
	}
	flag, step, start := "--log", "", ""
	switch {
	case s != nil:
		step = s.Name
		if s.bucket() == bucketFail {
			start = "+G"
		}
	case j.bucket() == bucketFail:
		flag, start = "--log-failed", "+G"
	}
	c := exec.Command("sh", "-c", logScript, "sh",
		strconv.FormatInt(j.ID, 10), v.list.Selected().repo(), flag, step, start)
	return tea.ExecProcess(c, func(error) tea.Msg { return nil })
}

// jobsBindings is the footer while the pane has focus: its own keys, since
// the PR list's no longer apply.
func (v *View) jobsBindings() []key.Binding {
	help := func(k, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc))
	}
	// Most telling first: a narrow footer keeps only the first four, and
	// how to get back out is the one that must survive.
	return []key.Binding{
		help("enter", "steps / log"),
		help("esc", "back to PRs"),
		help(v.keyHint("open_job"), "open"),
		help(v.keyHint("job_log"), "pager"),
		help(v.keyHint("rerun"), "rerun"),
		help(ui.HelpLabel(firstKey(v.nav.Down))+"/"+ui.HelpLabel(firstKey(v.nav.Up)), "move"),
	}
}

func firstKey(b key.Binding) string {
	if ks := b.Keys(); len(ks) > 0 {
		return ks[0]
	}
	return "?"
}

// --- pane rendering ---------------------------------------------------------

// renderedJobs renders the jobs pane for p and refreshes v.anchors, one per
// row, so the cursor and clicks can find each row's line. Not memoized: it is
// plain text, and the running durations should tick with every repaint.
func (v *View) renderedJobs(p pr) string {
	if v.logView != nil {
		return v.renderedLog()
	}
	st, rows, sel := v.jobsCursor()
	switch {
	case st == nil:
		v.anchors = nil
		return ui.Faint.Render("Loading jobs…")
	case st.err != nil && len(st.jobs) == 0:
		v.anchors = nil
		return ui.Red.Render(firstLine(st.err.Error())) + "\n" + ui.Faint.Render(retryNote(st))
	}
	text, anchors := renderJobsPane(st, rows, sel, v.jobsOpen, v.jobsFocus, v.stepLines, v.prevW, v.jobsHint(), time.Now())
	v.anchors = anchors
	return text
}

// retryNote says what happens next after a failed fetch: another try on its
// own, or the key that asks for one.
func retryNote(st *jobsState) string {
	switch {
	case st.inFlight:
		return "retrying…"
	case st.failures <= jobsRetries:
		return fmt.Sprintf("retrying in %ds", int(jobsRetryAfter.Seconds()))
	}
	return "ctrl+r to retry"
}

// jobsHint names the pane's keys, which are remappable. Unfocused it says how
// to get in; focused, the footer lists the rest.
func (v *View) jobsHint() string {
	if v.jobsFocus {
		return fmt.Sprintf("enter steps / log · %s open · %s pager · %s rerun · esc back",
			v.keyHint("open_job"), v.keyHint("job_log"), v.keyHint("rerun"))
	}
	return fmt.Sprintf("→ or click to browse jobs · %s/%s next failure · %s rerun",
		v.keyHint("next_thread"), v.keyHint("prev_thread"), v.keyHint("rerun"))
}

// stepContent reports how many lines a step printed, and whether that is
// known yet.
type stepContent func(j ciJob, s ciStep) (lines int, known bool)

func renderJobsPane(st *jobsState, rows []jobRow, sel int, open map[string]bool, focused bool,
	content stepContent, width int, hint string, now time.Time) (string, []ui.DiffAnchor) {
	var out []string
	var anchors []ui.DiffAnchor

	if len(st.jobs) == 0 {
		out = append(out, ui.Faint.Render("No checks on the head commit."))
		if st.err != nil {
			out = append(out, ui.Yellow.Render("refresh failed: "+st.err.Error()))
		}
		return strings.Join(out, "\n"), nil
	}

	out = append(out, jobsSummary(st.jobs))
	meta := shortSHA(st.sha)
	switch {
	case st.watching(now):
		meta += " · watching"
	case !st.fetchedAt.IsZero():
		if now.Sub(st.fetchedAt) < time.Minute {
			meta += " · updated just now"
		} else {
			meta += " · updated " + ui.Age(st.fetchedAt) + " ago"
		}
	}
	out = append(out, ui.Dim.Render(strings.TrimPrefix(meta, " · ")))
	if st.err != nil {
		out = append(out, ui.Yellow.Render("refresh failed: "+firstLine(st.err.Error())))
	}
	out = append(out, ui.Faint.Render(hint), "")

	group := ""
	for i, r := range rows {
		j := st.jobs[r.job]
		if j.groupKey != group {
			if group != "" {
				out = append(out, "")
			}
			group = j.groupKey
			head := ui.Bold.Render(j.Group)
			if j.RunNumber > 0 {
				head += ui.Dim.Render(fmt.Sprintf("  #%d", j.RunNumber))
			}
			out = append(out, head)
		}
		anchors = append(anchors, ui.DiffAnchor{Line: len(out), ID: strconv.Itoa(i)})
		if r.step < 0 {
			out = append(out, jobLine(j, open[j.key()], rowCursor(i == sel, focused), width, now))
		} else {
			n, known := content(j, j.Steps[r.step])
			out = append(out, stepLine(j.Steps[r.step], n, known, rowCursor(i == sel, focused), width, now))
		}
	}
	return strings.Join(out, "\n"), anchors
}

// jobsSummary counts the jobs by outcome, naming only the non-zero ones.
func jobsSummary(jobs []ciJob) string {
	counts := map[jobBucket]int{}
	for _, j := range jobs {
		counts[j.bucket()]++
	}
	var parts []string
	add := func(b jobBucket, word string) {
		if n := counts[b]; n > 0 {
			parts = append(parts, jobGlyph(b)+" "+jobStyle(b).Render(fmt.Sprintf("%d %s", n, word)))
		}
	}
	add(bucketFail, "failed")
	add(bucketCancelled, "cancelled")
	add(bucketRunning, "running")
	add(bucketQueued, "queued")
	add(bucketPass, "passed")
	add(bucketSkipped, "skipped")
	return strings.Join(parts, "   ")
}

// rowState is how a row is marked: not selected, selected in a pane that has
// focus, or selected while the keys are with the PR list.
type rowState int

const (
	rowPlain rowState = iota
	rowFocused
	rowUnfocused
)

func rowCursor(selected, focused bool) rowState {
	switch {
	case !selected:
		return rowPlain
	case focused:
		return rowFocused
	default:
		return rowUnfocused
	}
}

// cursorMark and nameStyle render a row's cursor and name for its state: the
// accent while the pane has focus, dim while the keys are elsewhere.
func (s rowState) cursorMark() string {
	switch s {
	case rowFocused:
		return ui.Accent.Render("› ")
	case rowUnfocused:
		return ui.Dim.Render("› ")
	}
	return "  "
}

func (s rowState) name(text string, plain lipgloss.Style) string {
	switch s {
	case rowFocused:
		return ui.Accent.Bold(true).Render(text)
	case rowUnfocused:
		return ui.Bold.Render(text)
	}
	return plain.Render(text)
}

// jobLine is one job: cursor, an open/closed marker when it has steps, glyph,
// name, required tag, and a right-aligned duration (or state, for a job that
// has not started).
func jobLine(j ciJob, open bool, state rowState, width int, now time.Time) string {
	// What enter does, at a glance: ▸/▾ opens into steps, ↗ leaves for the
	// check's own page, and nothing means there is nothing more to see.
	marker := "  "
	switch {
	case len(j.Steps) > 0 && open:
		marker = ui.Dim.Render("▾ ")
	case len(j.Steps) > 0:
		marker = ui.Dim.Render("▸ ")
	case j.URL != "":
		marker = ui.Dim.Render("↗ ")
	}
	glyph := jobGlyph(j.bucket())
	right := jobDuration(j, now)
	tag := ""
	if j.Required {
		tag = "  required"
	}
	// cursor(2) + marker(2) + glyph + space, then the name, which absorbs
	// whatever the width cannot fit around it.
	fixed := 4 + lipgloss.Width(glyph) + 1 + len(tag) + 1 + lipgloss.Width(right)
	nameW := max(8, width-fixed)
	name := ui.Truncate(j.Name, nameW)
	styled := state.name(name, lipgloss.NewStyle())
	used := lipgloss.Width(name)
	if j.Description != "" {
		// A commit status says what it did in its description; show what
		// fits of it after the name.
		if room := nameW - used - 2; room > 8 {
			d := ui.Truncate(j.Description, room)
			styled += "  " + ui.Dim.Render(d)
			used += 2 + lipgloss.Width(d)
		}
	}
	gap := max(1, width-(fixed-1)-used)
	return state.cursorMark() + marker + glyph + " " + styled + ui.Dim.Render(tag) +
		strings.Repeat(" ", gap) + ui.Dim.Render(right)
}

// stepLine is one step, indented under its job. Once its job's log has
// landed a step says how many lines it printed; one that printed nothing, or
// was skipped, is faint, since enter on it has nothing to show.
func stepLine(s ciStep, lines int, known bool, state rowState, width int, now time.Time) string {
	b := s.bucket()
	glyph := jobGlyph(b)
	right := stepDuration(s, now)
	empty := b == bucketSkipped || (known && lines == 0)
	if known && lines > 0 {
		count := fmt.Sprintf("%d lines", lines)
		if lines == 1 {
			count = "1 line"
		}
		right = strings.TrimSpace(count + "  " + right)
	}
	fixed := 2 + 4 + lipgloss.Width(glyph) + 1 + 1 + lipgloss.Width(right)
	name := ui.Truncate(s.Name, max(8, width-fixed))
	plain := lipgloss.NewStyle()
	if empty {
		plain = ui.Faint
	}
	gap := max(1, width-(fixed-1)-lipgloss.Width(name))
	return state.cursorMark() + "    " + glyph + " " + state.name(name, plain) +
		strings.Repeat(" ", gap) + ui.Dim.Render(right)
}

// stepDuration is a step's run time, or how long a running step has been
// going.
func stepDuration(s ciStep, now time.Time) string {
	switch {
	case s.bucket() == bucketRunning && !s.StartedAt.IsZero():
		return fmtDuration(now.Sub(s.StartedAt)) + "…"
	case s.StartedAt.IsZero() || !s.CompletedAt.After(s.StartedAt):
		return ""
	}
	return fmtDuration(s.CompletedAt.Sub(s.StartedAt))
}

func jobGlyph(b jobBucket) string {
	switch b {
	case bucketFail:
		return ui.Red.Render(ui.IconCIFail)
	case bucketCancelled:
		return ui.Dim.Render(ui.IconCIFail)
	case bucketRunning:
		return ui.Yellow.Render(ui.IconCIPending)
	case bucketQueued:
		return ui.Dim.Render(ui.IconCIPending)
	case bucketPass:
		return ui.Green.Render(ui.IconCIOK)
	case bucketUnreported:
		return ui.Dim.Render(ui.IconDot)
	default: // skipped, neutral, stale
		return ui.Dim.Render(ui.IconCISkipped)
	}
}

func jobStyle(b jobBucket) lipgloss.Style {
	switch b {
	case bucketFail:
		return ui.Red
	case bucketRunning:
		return ui.Yellow
	case bucketPass:
		return ui.Green
	default:
		return ui.Dim
	}
}

// jobDuration is how long a finished job took, how long a running one has
// been going, or the state of one that has not started.
func jobDuration(j ciJob, now time.Time) string {
	switch j.bucket() {
	case bucketQueued:
		if j.Status == "WAITING" {
			return "waiting"
		}
		return "queued"
	case bucketRunning:
		if j.StartedAt.IsZero() {
			return "running"
		}
		return fmtDuration(now.Sub(j.StartedAt)) + "…"
	}
	if j.StartedAt.IsZero() || !j.CompletedAt.After(j.StartedAt) {
		return ""
	}
	return fmtDuration(j.CompletedAt.Sub(j.StartedAt))
}

func fmtDuration(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// --- rerun ------------------------------------------------------------------

// rerunFlow is the rerun popup ('x'): what to rerun, picked by cursor or
// hotkey. The options are derived from the jobs as they are on screen, so a
// watch refetch that lands while the popup is open updates them.
type rerunFlow struct {
	url, repo  string
	num        int
	sel        int
	submitting bool
}

type rerunOption struct {
	key, label string
	// args are the gh invocations the option runs, in order; what is the
	// flash on success. An option without args is Cancel.
	args [][]string
	what string
}

type rerunDoneMsg struct {
	from *View
	url  string
	what string
	err  error
}

// rerunOptions lists what can be rerun right now, and why not when nothing
// can. Only GitHub Actions runs can be rerun through gh, and only once they
// have finished: GitHub refuses a rerun of a run still in progress.
func (v *View) rerunOptions() ([]rerunOption, string) {
	r := v.rerun
	st, ok := v.jobs[r.url]
	if !ok || !st.done {
		return []rerunOption{{key: "esc", label: "Cancel"}}, "loading jobs…"
	}

	type runInfo struct {
		name   string
		failed bool
		active bool
	}
	runs := map[int64]*runInfo{}
	var order []int64
	for _, j := range st.jobs {
		if !j.Actions {
			continue
		}
		ri, seen := runs[j.RunID]
		if !seen {
			ri = &runInfo{name: j.Group}
			runs[j.RunID] = ri
			order = append(order, j.RunID)
		}
		b := j.bucket()
		ri.failed = ri.failed || b == bucketFail || b == bucketCancelled
		ri.active = ri.active || j.active()
	}

	var opts []rerunOption
	var failedArgs [][]string
	var failedNames []string
	for _, id := range order {
		if ri := runs[id]; ri.failed && !ri.active {
			failedArgs = append(failedArgs, []string{"run", "rerun", strconv.FormatInt(id, 10), "--failed", "-R", r.repo})
			failedNames = append(failedNames, ri.name)
		}
	}
	if len(failedArgs) > 0 {
		label := "Rerun failed jobs in " + failedNames[0]
		if len(failedNames) > 1 {
			label = fmt.Sprintf("Rerun failed jobs in %d workflows", len(failedNames))
		}
		opts = append(opts, rerunOption{key: "f", label: label, args: failedArgs,
			what: fmt.Sprintf("rerunning failed jobs on %s#%d", r.repo, r.num)})
	}

	// The cursor's job only counts while it belongs to the popup's PR: the
	// mouse can move the selection under an open popup.
	j, ok := v.currentJob()
	ri := runs[j.RunID]
	if ok && j.Actions && v.list.Selected().URL == r.url && ri != nil && !ri.active {
		// Hotkeys stay off j/k, which move the popup's cursor.
		opts = append(opts,
			rerunOption{key: "r", label: "Rerun " + j.Name,
				args: [][]string{{"run", "rerun", "--job", strconv.FormatInt(j.ID, 10), "-R", r.repo}},
				what: "rerunning " + j.Name},
			rerunOption{key: "a", label: "Rerun all jobs in " + j.Group,
				args: [][]string{{"run", "rerun", strconv.FormatInt(j.RunID, 10), "-R", r.repo}},
				what: "rerunning " + j.Group})
	}

	why := ""
	if len(opts) == 0 {
		switch {
		case len(runs) == 0:
			why = "only GitHub Actions jobs can be rerun from here"
		default:
			why = "nothing has finished yet: GitHub reruns a workflow once it completes"
		}
	}
	return append(opts, rerunOption{key: "esc", label: "Cancel"}), why
}

// openRerun opens the rerun popup, showing the jobs pane first so there is
// something to pick from.
func (v *View) openRerun() tea.Cmd {
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	v.rerun = &rerunFlow{url: p.URL, repo: p.repo(), num: p.Number}
	if v.pane == paneJobs {
		return nil
	}
	return v.setPane(paneJobs)
}

// updateRerun handles keys while the rerun popup is open.
func (v *View) updateRerun(msg tea.KeyMsg) tea.Cmd {
	r := v.rerun
	if r.submitting {
		return nil
	}
	opts, _ := v.rerunOptions()
	r.sel = min(r.sel, len(opts)-1)
	switch msg.String() {
	case "up", "k":
		r.sel = (r.sel - 1 + len(opts)) % len(opts)
	case "down", "j":
		r.sel = (r.sel + 1) % len(opts)
	case "enter":
		return v.activateRerun(opts[r.sel])
	case "esc", "q", "ctrl+c":
		v.rerun = nil
	default:
		return v.runRerun(opts, msg.String())
	}
	return nil
}

// runRerun activates the option bound to hotkey k, if there is one.
func (v *View) runRerun(opts []rerunOption, k string) tea.Cmd {
	for _, o := range opts {
		if o.key == k && k != "esc" {
			return v.activateRerun(o)
		}
	}
	return nil
}

func (v *View) activateRerun(o rerunOption) tea.Cmd {
	if len(o.args) == 0 {
		v.rerun = nil
		return nil
	}
	v.rerun.submitting = true
	url, what, args := v.rerun.url, o.what, o.args
	return func() tea.Msg {
		for _, a := range args {
			if out, err := ghCombined(a...); err != nil {
				return rerunDoneMsg{from: v, url: url, err: ghErr(err, out)}
			}
		}
		return rerunDoneMsg{from: v, url: url, what: what}
	}
}

// applyRerun closes the popup and starts watching for the new attempt.
func (v *View) applyRerun(msg rerunDoneMsg) tea.Cmd {
	v.rerun = nil
	if msg.err != nil {
		v.flash = ui.Red.Render("rerun failed: " + msg.err.Error())
		return statusCmd(ui.SeverityError, msg.err)
	}
	v.flash = ui.Green.Render("✓ " + msg.what)
	if st, ok := v.jobs[msg.url]; ok {
		st.rerunAt = time.Now()
		st.fetchedAt = time.Time{} // stale: the next tick refetches
	}
	// A short first wait: GitHub queues the new attempt within seconds.
	return v.scheduleJobsWatch(msg.url, 3*time.Second)
}

// rerunOverlay renders the rerun popup.
func (v *View) rerunOverlay() string {
	r := v.rerun
	var b strings.Builder
	b.WriteString(ui.Bold.Render(fmt.Sprintf("Rerun %s#%d", r.repo, r.num)))
	b.WriteString("\n\n")
	if r.submitting {
		b.WriteString(ui.Faint.Render("asking GitHub to rerun…"))
	} else {
		opts, why := v.rerunOptions()
		if why != "" {
			b.WriteString(ui.Yellow.Render("! "+why) + "\n\n")
		}
		sel := min(r.sel, len(opts)-1)
		for i, o := range opts {
			cursor, label := "  ", o.label
			if i == sel {
				cursor, label = ui.Accent.Render("› "), ui.Accent.Render(label)
			}
			hot := "   "
			if o.key != "esc" {
				hot = ui.Bold.Render(o.key) + "  "
			}
			b.WriteString(cursor + hot + label + "\n")
		}
		b.WriteString("\n" + ui.Dim.Render("↑↓ move · enter select · esc close"))
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Render(b.String())
}
