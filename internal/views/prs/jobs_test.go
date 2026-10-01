package prs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// run builds a GitHub Actions check run context.
func run(id, runID int64, workflow, event, name, status, conclusion string, steps ...ciStep) rawContext {
	c := rawContext{Typename: "CheckRun", DatabaseID: id, Name: name, Status: status, Conclusion: conclusion,
		DetailsURL: "https://github.com/o/r/actions/runs/1/job/" + name}
	c.CheckSuite.WorkflowRun = &struct {
		DatabaseID int64  `json:"databaseId"`
		RunNumber  int    `json:"runNumber"`
		Event      string `json:"event"`
		Workflow   struct {
			Name string `json:"name"`
		} `json:"workflow"`
	}{DatabaseID: runID, RunNumber: 7, Event: event}
	c.CheckSuite.WorkflowRun.Workflow.Name = workflow
	c.Steps.Nodes = steps
	return c
}

func status(context, state, desc string) rawContext {
	return rawContext{Typename: "StatusContext", Context: context, State: state, Description: desc}
}

func names(jobs []ciJob) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Group + "/" + j.Name
	}
	return out
}

// A rerun adds a second check run under the same name; only the newer
// attempt counts, or the pane would show a job as both failed and passing.
func TestToJobsKeepsNewestAttempt(t *testing.T) {
	jobs := toJobs([]rawContext{
		run(10, 1, "CI", "pull_request", "test", "COMPLETED", "FAILURE"),
		run(20, 1, "CI", "pull_request", "test", "COMPLETED", "SUCCESS"),
	})
	if len(jobs) != 1 || jobs[0].ID != 20 || jobs[0].Conclusion != "SUCCESS" {
		t.Fatalf("got %+v, want only the newer attempt (id 20, SUCCESS)", jobs)
	}
}

// Groups are ordered by their worst job and jobs by their own state, so a
// failure is the first thing in the pane wherever it sits.
func TestToJobsSortsWorstFirst(t *testing.T) {
	jobs := toJobs([]rawContext{
		run(1, 1, "Lint", "pull_request", "eslint", "COMPLETED", "SUCCESS"),
		run(2, 2, "CI", "pull_request", "build", "COMPLETED", "SUCCESS"),
		run(3, 2, "CI", "pull_request", "test", "COMPLETED", "FAILURE"),
		run(4, 2, "CI", "pull_request", "e2e", "IN_PROGRESS", ""),
		status("vercel", "SUCCESS", "Deployed"),
	})
	got := strings.Join(names(jobs), ", ")
	want := "CI/test, CI/e2e, CI/build, Lint/eslint, Statuses/vercel"
	if got != want {
		t.Errorf("order = %s\nwant    %s", got, want)
	}
}

// One workflow on both push and pull_request is two runs; the heading says
// which is which rather than merging their jobs.
func TestToJobsLabelsSameWorkflowByEvent(t *testing.T) {
	jobs := toJobs([]rawContext{
		run(1, 1, "CI", "push", "test", "COMPLETED", "SUCCESS"),
		run(2, 2, "CI", "pull_request", "test", "COMPLETED", "SUCCESS"),
	})
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want both runs kept", len(jobs))
	}
	groups := map[string]bool{}
	for _, j := range jobs {
		groups[j.Group] = true
	}
	if !groups["CI · push"] || !groups["CI · pull_request"] {
		t.Errorf("groups = %v, want the event on each", groups)
	}
}

func TestToJobsMapsStatusContexts(t *testing.T) {
	jobs := toJobs([]rawContext{status("ci/circleci", "PENDING", "running"), status("deploy", "ERROR", "boom")})
	byName := map[string]ciJob{}
	for _, j := range jobs {
		byName[j.Name] = j
	}
	if b := byName["ci/circleci"].bucket(); b != bucketQueued {
		t.Errorf("pending status bucket = %v, want queued", b)
	}
	if b := byName["deploy"].bucket(); b != bucketFail {
		t.Errorf("error status bucket = %v, want failed", b)
	}
	if byName["deploy"].Actions {
		t.Error("a commit status is not a GitHub Actions job")
	}
}

// jobsView is a PR view with one PR selected and gh stubbed out.
func jobsView(t *testing.T, contexts ...rawContext) (*View, *[][]string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var c rawCommit
	c.OID = "abcdef1234567"
	c.StatusCheckRollup.Contexts.Nodes = contexts
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	calls := &[][]string{}
	prevOut, prevComb := ghOutput, ghCombined
	ghOutput = func(args ...string) ([]byte, error) {
		*calls = append(*calls, args)
		if len(args) > 0 && args[0] == "run" {
			return []byte(logFixture), nil
		}
		return body, nil
	}
	ghCombined = func(args ...string) ([]byte, error) {
		*calls = append(*calls, args)
		return nil, nil
	}
	prevBackoff := ghBackoff
	ghBackoff = func(time.Duration) {}
	t.Cleanup(func() { ghOutput, ghCombined, ghBackoff = prevOut, prevComb, prevBackoff })

	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.SetSize(80, 70, 40)
	p1 := pr{Number: 1, URL: "u1", Title: "one"}
	p1.Repository.NameWithOwner = "o/r"
	p2 := pr{Number: 2, URL: "u2", Title: "two"}
	p2.Repository.NameWithOwner = "o/r"
	v.Update(mineMsg{page: searchPage{prs: []pr{p1, p2}}})
	return v, calls
}

// openJobs toggles the pane and delivers the fetch it starts, returning what
// landing it asked for next (the logs of the steps on screen, say).
func openJobs(t *testing.T, v *View) tea.Cmd {
	t.Helper()
	v.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	if v.pane != paneJobs {
		t.Fatalf("pane = %v after t, want jobs", v.pane)
	}
	st, ok := v.jobs["u1"]
	if !ok || !st.inFlight {
		t.Fatal("opening the pane did not start a jobs fetch")
	}
	cmd := fetchJobsCmd("u1", "o/r", 1)
	return v.Update(cmd())
}

func TestJobsPaneRendersFailuresWithSteps(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "build", "COMPLETED", "SUCCESS"),
		run(2, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE",
			ciStep{Number: 1, Name: "Set up job", Status: "COMPLETED", Conclusion: "SUCCESS"},
			ciStep{Number: 4, Name: "Run go test", Status: "COMPLETED", Conclusion: "FAILURE"}),
	)
	openJobs(t, v)
	out := ansi.Strip(v.PreviewView())
	for _, want := range []string{"1 failed", "1 passed", "abcdef1", "CI", "› ", "test", "Run go test", "build"} {
		if !strings.Contains(out, want) {
			t.Errorf("pane missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Set up job") {
		t.Errorf("a passed step of a failed job should not be listed:\n%s", out)
	}
	if strings.Index(out, "test") > strings.Index(out, "build") {
		t.Errorf("the failed job should come before the passing one:\n%s", out)
	}
}

// The jobs pane has no use for comments; fetching them would be a wasted
// request per PR.
func TestJobsPaneDoesNotFetchComments(t *testing.T) {
	v, _ := jobsView(t)
	v.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	if _, ok := v.comments["u1"]; ok {
		t.Error("the jobs pane started a comments fetch")
	}
}

func TestClickSelectsJob(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "a", "COMPLETED", "FAILURE"),
		run(2, 9, "CI", "pull_request", "b", "COMPLETED", "SUCCESS"),
	)
	openJobs(t, v)
	lines := strings.Split(ansi.Strip(v.PreviewView()), "\n")
	target := -1
	for i, l := range lines {
		if strings.Contains(l, " b ") || strings.HasSuffix(strings.TrimSpace(l), " b") {
			target = i
		}
	}
	if target < 0 {
		t.Fatalf("no line for job b in:\n%s", strings.Join(lines, "\n"))
	}
	v.ClickPreview(target, 0)
	if j, _ := v.currentJob(); j.Name != "b" {
		t.Errorf("click on b left the cursor on %q", j.Name)
	}
}

// While anything runs the pane keeps polling, and a tick that belongs to an
// older chain or another row does nothing.
func TestWatchFollowsRunningJobs(t *testing.T) {
	v, calls := jobsView(t, run(1, 9, "CI", "pull_request", "test", "IN_PROGRESS", ""))
	v.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	cmd := v.Update(fetchJobsCmd("u1", "o/r", 1)())
	if cmd == nil {
		t.Fatal("a running job should schedule a watch tick")
	}
	gen := v.jobsGen

	v.jobs["u1"].fetchedAt = time.Now().Add(-jobsWatchEvery)
	before := len(*calls)
	if c := v.Update(jobsTickMsg{url: "u1", gen: gen - 1}); c != nil {
		t.Error("a superseded tick should do nothing")
	}
	if c := v.Update(jobsTickMsg{url: "u2", gen: gen}); c != nil {
		t.Error("a tick for another row should do nothing")
	}
	c := v.Update(jobsTickMsg{url: "u1", gen: gen})
	if c == nil {
		t.Fatal("the current tick should refetch a stale, running pane")
	}
	c()
	if len(*calls) != before+1 {
		t.Errorf("gh calls = %d, want one refetch", len(*calls)-before)
	}
}

// Once the last job finishes the list row's CI state is stale, so the row is
// re-read; with nothing running the watch stops.
func TestWatchStopsAndRefreshesRowWhenDone(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "test", "IN_PROGRESS", ""))
	v.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	v.Update(fetchJobsCmd("u1", "o/r", 1)())
	gen := v.jobsGen

	done := toJobs([]rawContext{run(1, 9, "CI", "pull_request", "test", "COMPLETED", "SUCCESS")})
	v.jobs["u1"].inFlight = true
	if cmd := v.Update(jobsMsg{url: "u1", jobs: done, sha: "abc"}); cmd == nil {
		t.Error("finishing should re-read the row")
	}
	if v.jobsGen != gen {
		t.Error("nothing is running: the watch should not have been rescheduled")
	}
}

// A failed refetch keeps what is on screen and says so, rather than blanking
// a pane that was fine a moment ago.
func TestRefetchErrorKeepsJobs(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE"))
	openJobs(t, v)
	v.jobs["u1"].inFlight = true
	v.Update(jobsMsg{url: "u1", err: errors.New("HTTP 502")})
	out := ansi.Strip(v.PreviewView())
	if !strings.Contains(out, "test") || !strings.Contains(out, "refresh failed: HTTP 502") {
		t.Errorf("want the old jobs plus the failure:\n%s", out)
	}
}

func TestRerunFailedJobs(t *testing.T) {
	v, calls := jobsView(t,
		run(1, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE"),
		run(2, 9, "CI", "pull_request", "build", "COMPLETED", "SUCCESS"),
	)
	openJobs(t, v)
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if v.rerun == nil {
		t.Fatal("x did not open the rerun popup")
	}
	if !strings.Contains(ansi.Strip(v.Overlay()), "Rerun failed jobs in CI") {
		t.Errorf("popup missing the failed-jobs option:\n%s", ansi.Strip(v.Overlay()))
	}
	cmd := v.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if cmd == nil {
		t.Fatal("f should submit the rerun")
	}
	before := len(*calls)
	msg := cmd()
	got := strings.Join((*calls)[before], " ")
	if got != "run rerun 9 --failed -R o/r" {
		t.Errorf("gh args = %q", got)
	}
	if next := v.Update(msg); next == nil {
		t.Error("a successful rerun should schedule a refetch")
	}
	if v.rerun != nil {
		t.Error("the popup should close once the rerun is requested")
	}
	st := v.jobs["u1"]
	if st.rerunAt.IsZero() || !st.stale(time.Now()) || !st.watching(time.Now()) {
		t.Error("after a rerun the pane should be stale and watching for the new attempt")
	}
}

func TestRerunThisJob(t *testing.T) {
	v, calls := jobsView(t, run(42, 9, "CI", "pull_request", "test", "COMPLETED", "SUCCESS"))
	openJobs(t, v)
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	cmd := v.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("r should rerun the job under the cursor")
	}
	before := len(*calls)
	cmd()
	if got := strings.Join((*calls)[before], " "); got != "run rerun --job 42 -R o/r" {
		t.Errorf("gh args = %q", got)
	}
}

// GitHub refuses to rerun a run still in progress, so the popup says why
// instead of offering something that will fail.
func TestRerunRefusedWhileRunning(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE"),
		run(2, 9, "CI", "pull_request", "e2e", "IN_PROGRESS", ""),
	)
	openJobs(t, v)
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	out := ansi.Strip(v.Overlay())
	if strings.Contains(out, "Rerun failed") || !strings.Contains(out, "nothing has finished yet") {
		t.Errorf("popup should refuse with the reason:\n%s", out)
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}); cmd != nil {
		t.Error("f must not submit while the run is in progress")
	}
}

func TestRerunRefusedForExternalChecks(t *testing.T) {
	v, _ := jobsView(t, status("ci/circleci: build", "FAILURE", "failed"))
	openJobs(t, v)
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if out := ansi.Strip(v.Overlay()); !strings.Contains(out, "only GitHub Actions jobs") {
		t.Errorf("popup should say why:\n%s", out)
	}
}

// x from the description opens the jobs pane too, so the popup has jobs to
// offer once they load.
func TestRerunOpensJobsPane(t *testing.T) {
	v, _ := jobsView(t)
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if v.pane != paneJobs || v.rerun == nil {
		t.Errorf("pane = %v, rerun = %v; want the jobs pane under the popup", v.pane, v.rerun)
	}
	if !v.InputActive() {
		t.Error("the popup should hold the keyboard")
	}
}

func TestFmtDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                             "0s",
		45 * time.Second:              "45s",
		3*time.Minute + 2*time.Second: "3m02s",
		time.Hour + 4*time.Minute:     "1h04m",
	} {
		if got := fmtDuration(d); got != want {
			t.Errorf("fmtDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// A failed fetch retries on its own a couple of times, then waits for you to
// come back to the pane rather than polling an error forever.
func TestFailedFetchRetriesThenStops(t *testing.T) {
	v, _ := jobsView(t)
	v.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	for i := 1; i <= jobsRetries; i++ {
		v.jobs["u1"].inFlight = true
		if cmd := v.Update(jobsMsg{url: "u1", err: errors.New("HTTP 502")}); cmd == nil {
			t.Fatalf("failure %d should schedule a retry", i)
		}
	}
	v.jobs["u1"].inFlight = true
	if cmd := v.Update(jobsMsg{url: "u1", err: errors.New("HTTP 502")}); cmd != nil {
		t.Error("past the retry budget the pane should stop retrying")
	}
	if !v.jobs["u1"].stale(time.Now()) {
		t.Error("an errored pane should refetch the next time it opens")
	}
}

// The mouse can move the selection under an open popup; the popup keeps
// offering reruns for its own PR, not the job now under the cursor.
func TestRerunPopupIgnoresOtherPRsJob(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE"))
	v.Update(ui.TogglesPersistMsg(true))
	openJobs(t, v)
	v.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	v.jobs["u2"] = &jobsState{done: true, fetchedAt: time.Now(),
		jobs: toJobs([]rawContext{run(5, 77, "Other", "push", "deploy", "COMPLETED", "SUCCESS")})}
	v.ScrollList(1)
	if v.list.Selected().URL != "u2" {
		t.Fatalf("selected %s, want u2", v.list.Selected().URL)
	}
	out := ansi.Strip(v.Overlay())
	if strings.Contains(out, "deploy") || !strings.Contains(out, "Rerun failed jobs in CI") {
		t.Errorf("popup should only offer u1's reruns:\n%s", out)
	}
}

func pressKeys(v *View, keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter":
			v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		case "left":
			v.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		case "right":
			v.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		default:
			r := []rune(k)[0]
			v.Update(tea.KeyPressMsg{Code: r, Text: k})
		}
	}
}

func cursorAt(t *testing.T, v *View) string {
	t.Helper()
	j, s, ok := v.currentRow()
	if !ok {
		return ""
	}
	if s != nil {
		return j.Name + "/" + s.Name
	}
	return j.Name
}

var threeSteps = []ciStep{
	{Number: 1, Name: "Set up job", Status: "COMPLETED", Conclusion: "SUCCESS"},
	{Number: 2, Name: "Build", Status: "COMPLETED", Conclusion: "SUCCESS"},
	{Number: 3, Name: "Complete job", Status: "COMPLETED", Conclusion: "SUCCESS"},
}

// Opening the pane gives it the keys: j/k walk the jobs and leave the PR
// list alone until esc hands the keys back.
func TestFocusedPaneTakesMovementKeys(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "a", "COMPLETED", "FAILURE"),
		run(2, 9, "CI", "pull_request", "b", "COMPLETED", "SUCCESS"),
		run(3, 9, "CI", "pull_request", "c", "COMPLETED", "SUCCESS"),
	)
	openJobs(t, v)
	if !v.jobsFocus {
		t.Fatal("t should focus the jobs pane")
	}
	pressKeys(v, "j", "j")
	if got := cursorAt(t, v); got != "c" {
		t.Errorf("after j j cursor on %q, want c", got)
	}
	pressKeys(v, "j")
	if got := cursorAt(t, v); got != "c" {
		t.Errorf("j past the end moved to %q, want it to stay on c", got)
	}
	pressKeys(v, "g")
	if got := cursorAt(t, v); got != "a" {
		t.Errorf("g put the cursor on %q, want a", got)
	}
	if v.list.Selected().URL != "u1" {
		t.Fatalf("PR selection moved to %s while the pane had focus", v.list.Selected().URL)
	}

	pressKeys(v, "esc")
	if v.jobsFocus || v.pane != paneJobs {
		t.Fatalf("esc: focus %v pane %v, want the pane kept and focus back on the list", v.jobsFocus, v.pane)
	}
	pressKeys(v, "right")
	if !v.jobsFocus {
		t.Fatal("right arrow should focus the pane again")
	}
	pressKeys(v, "esc", "j")
	if v.list.Selected().URL != "u2" || v.pane != paneBody {
		t.Errorf("after esc, j should move the PR list (got %s) and fold the pane (got %v)",
			v.list.Selected().URL, v.pane)
	}
}

// enter opens a job into its steps, which the cursor then walks; left goes
// up to the job, then closes it, then leaves the pane, as a tree does.
func TestEnterOpensJobIntoSteps(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "build", "COMPLETED", "SUCCESS", threeSteps...))
	openJobs(t, v)
	if strings.Contains(ansi.Strip(v.PreviewView()), "Set up job") {
		t.Fatal("a passing job's steps should stay folded until opened")
	}
	pressKeys(v, "enter")
	if out := ansi.Strip(v.PreviewView()); !strings.Contains(out, "Set up job") || !strings.Contains(out, "▾") {
		t.Fatalf("enter should list the job's steps:\n%s", out)
	}
	pressKeys(v, "j", "j")
	if got := cursorAt(t, v); got != "build/Build" {
		t.Errorf("cursor on %q, want the second step", got)
	}
	if u := v.jobURL(); !strings.HasSuffix(u, "#step:2:1") {
		t.Errorf("a step's URL = %q, want the job page at that step", u)
	}

	pressKeys(v, "left")
	if got := cursorAt(t, v); got != "build" {
		t.Errorf("left from a step went to %q, want its job", got)
	}
	pressKeys(v, "left")
	if strings.Contains(ansi.Strip(v.PreviewView()), "Set up job") {
		t.Error("left on an open job should close it")
	}
	pressKeys(v, "left")
	if v.jobsFocus {
		t.Error("left on a closed job should hand the keys back to the list")
	}
}

// A failed job lists its failed steps without being opened, and they are rows
// like any other: the cursor can stop on one and page just that step's log.
func TestFailedStepIsARow(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE",
		ciStep{Number: 1, Name: "Set up job", Status: "COMPLETED", Conclusion: "SUCCESS"},
		ciStep{Number: 4, Name: "Run go test", Status: "COMPLETED", Conclusion: "FAILURE"}))
	openJobs(t, v)
	pressKeys(v, "j")
	if got := cursorAt(t, v); got != "test/Run go test" {
		t.Fatalf("cursor on %q, want the failed step", got)
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Error("enter on a step should page its log")
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}); cmd == nil {
		t.Error("p on a step should page its log")
	}
}

// ]/[ skip to the failures, wrapping, and focus the pane on the way.
func TestJumpBetweenFailures(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "a", "COMPLETED", "FAILURE"),
		run(2, 9, "CI", "pull_request", "b", "COMPLETED", "SUCCESS"),
		run(3, 8, "Lint", "pull_request", "c", "COMPLETED", "FAILURE"),
	)
	openJobs(t, v)
	pressKeys(v, "esc", "]")
	if got := cursorAt(t, v); got != "c" || !v.jobsFocus {
		t.Errorf("] went to %q (focus %v), want the next failure c, focused", got, v.jobsFocus)
	}
	pressKeys(v, "]")
	if got := cursorAt(t, v); got != "a" {
		t.Errorf("] from the last failure went to %q, want it wrapped to a", got)
	}
}

// A watch refetch re-sorts the jobs as they finish; the cursor stays on the
// job it was on rather than on whatever now sits at its index.
func TestCursorFollowsJobAcrossRefetch(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "a", "IN_PROGRESS", ""),
		run(2, 9, "CI", "pull_request", "b", "IN_PROGRESS", ""),
	)
	openJobs(t, v)
	pressKeys(v, "j")
	if got := cursorAt(t, v); got != "b" {
		t.Fatalf("cursor on %q, want b", got)
	}
	v.jobs["u1"].inFlight = true
	v.Update(jobsMsg{url: "u1", sha: "abc", jobs: toJobs([]rawContext{
		run(1, 9, "CI", "pull_request", "a", "COMPLETED", "SUCCESS"),
		run(2, 9, "CI", "pull_request", "b", "COMPLETED", "FAILURE"), // now sorts first
	})})
	if got := cursorAt(t, v); got != "b" {
		t.Errorf("after the refetch the cursor is on %q, want it still on b", got)
	}
}

func TestClickSelectedJobOpensIt(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "build", "COMPLETED", "SUCCESS", threeSteps...))
	openJobs(t, v)
	lines := strings.Split(ansi.Strip(v.PreviewView()), "\n")
	for i, l := range lines {
		if strings.Contains(l, "build") && strings.Contains(l, "▸") {
			v.ClickPreview(i, 0)
			break
		}
	}
	if !strings.Contains(ansi.Strip(v.PreviewView()), "Set up job") {
		t.Error("a click on the selected job should open it")
	}
}

func TestFooterFollowsFocus(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "a", "COMPLETED", "SUCCESS"))
	openJobs(t, v)
	has := func(desc string) bool {
		for _, b := range v.Bindings() {
			if b.Help().Desc == desc {
				return true
			}
		}
		return false
	}
	if !has("back to PRs") {
		t.Error("focused footer should list the pane's keys")
	}
	pressKeys(v, "esc")
	if has("back to PRs") || !has("jobs") {
		t.Error("unfocused footer should be the PR list's")
	}
}

func TestJobLogKey(t *testing.T) {
	v, _ := jobsView(t,
		run(1, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE"),
		run(2, 9, "CI", "pull_request", "e2e", "IN_PROGRESS", ""),
		status("vercel", "SUCCESS", "Deployed"),
	)
	openJobs(t, v)
	if cmd := v.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}); cmd == nil {
		t.Error("p on a failed Actions job should page its log")
	}
	pressKeys(v, "j") // e2e, running
	if cmd := v.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}); cmd != nil || !strings.Contains(ansi.Strip(v.flash), "still running") {
		t.Errorf("p on a running job should explain, flash = %q", ansi.Strip(v.flash))
	}
	pressKeys(v, "j") // vercel, a status
	if cmd := v.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}); cmd != nil || !strings.Contains(ansi.Strip(v.flash), "no log") {
		t.Errorf("p on a commit status should explain, flash = %q", ansi.Strip(v.flash))
	}
}

// Leaving the PR folds the pane like the others, and the watch for it ends.
func TestJobsPaneResetsOnMove(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "test", "IN_PROGRESS", ""))
	openJobs(t, v)
	gen := v.jobsGen
	pressKeys(v, "esc", "j")
	if v.pane != paneBody {
		t.Errorf("pane = %v after moving on, want the body", v.pane)
	}
	if cmd := v.Update(jobsTickMsg{url: "u1", gen: gen}); cmd != nil {
		t.Error("a tick for the PR left behind should do nothing")
	}
}

// GitHub can leave a finished job's steps at pending; they must not read as
// running (with a ticking duration) or as skipped.
func TestStepsOfAFinishedJobNeverRun(t *testing.T) {
	jobs := toJobs([]rawContext{run(1, 9, "CI", "pull_request", "review", "COMPLETED", "SUCCESS",
		ciStep{Number: 1, Name: "Set up job", Status: "COMPLETED", Conclusion: "SUCCESS"},
		ciStep{Number: 3, Name: "Review", Status: "PENDING", StartedAt: time.Now().Add(-time.Hour)})})
	s := jobs[0].Steps[1]
	if b := s.bucket(); b != bucketUnreported {
		t.Errorf("bucket = %v, want unreported", b)
	}
	if d := stepDuration(s, time.Now()); d != "" {
		t.Errorf("duration = %q, want none for a step that never reported", d)
	}
}

// The PR whose jobs are showing is the one row the root leaves lit: it says
// where that row landed in ListView, under the header line.
func TestFocusKeepsTheSelectedPRLines(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "a", "COMPLETED", "SUCCESS"))
	openJobs(t, v)
	pressKeys(v, "esc", "j", "t") // the second PR; moving folds the pane, so reopen it
	lines := strings.Split(v.ListView(), "\n")
	first, n, ok := v.FocusKeepLines()
	if !ok || n != 2 {
		t.Fatalf("FocusKeepLines = %d,%d,%v, want the two lines of a row", first, n, ok)
	}
	if !strings.Contains(ansi.Strip(lines[first+1]), "two") {
		t.Errorf("kept lines %d-%d are not the selected PR:\n%s", first, first+n-1, ansi.Strip(v.ListView()))
	}
}

// Clicks in the jobs pane work it rather than close it: a click on its
// summary, a heading or a blank line hands it the keys and leaves it open.
func TestClickInJobsPaneNeverClosesIt(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "a", "COMPLETED", "SUCCESS"))
	openJobs(t, v)
	pressKeys(v, "esc")
	v.PreviewView()
	v.ClickPreview(v.paneHeader, 0) // the summary line
	if v.pane != paneJobs || !v.jobsFocus {
		t.Errorf("pane %v focus %v after a click on the summary, want it open and focused", v.pane, v.jobsFocus)
	}
}
