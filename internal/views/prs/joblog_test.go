package prs

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/ui"
)

// logFixture is what the gh stub answers `gh run view --log` with: gh's
// job<TAB>step<TAB>timestamp layout, a BOM on the first line, and colour
// codes defanged the way gh prints them.
var logFixture = "test\tSet up job\t\ufeff2026-09-30T10:30:59.1Z Runner version 2.3\n" +
	"test\tRun go test\t2026-09-30T10:31:00.1Z ##[group]Run go test ./...\n" +
	"test\tRun go test\t2026-09-30T10:31:00.2Z ^[[36;1mgo test ./...^[[0m\n" +
	"test\tRun go test\t2026-09-30T10:31:00.3Z ##[endgroup]\n" +
	"test\tRun go test\t2026-09-30T10:31:01.0Z --- FAIL: TestThing\n" +
	"test\tRun go test\t2026-09-30T10:31:02.0Z ##[error]Process completed with exit code 1.\n"

var failingSteps = []ciStep{
	{Number: 1, Name: "Set up job", Status: "COMPLETED", Conclusion: "SUCCESS"},
	{Number: 2, Name: "Cache", Status: "COMPLETED", Conclusion: "SKIPPED"},
	{Number: 3, Name: "Quiet", Status: "COMPLETED", Conclusion: "SUCCESS"},
	{Number: 4, Name: "Run go test", Status: "COMPLETED", Conclusion: "FAILURE"},
}

// deliver runs a command and feeds every message it produces back into the
// view, the way the program loop would (batches included).
func deliver(v *View, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			deliver(v, c)
		}
	case nil:
	default:
		deliver(v, v.Update(msg))
	}
}

func TestParseLogCleansLines(t *testing.T) {
	lines := parseLog([]byte(logFixture + "test\tRun go test\t2026-09-30T10:31:03.0Z evil\x1b]0;title\x07 \x1b[2Jdone\r\n"))
	if lines[0].step != "Set up job" || lines[0].text != "Runner version 2.3" {
		t.Errorf("first line = %+v, want the BOM and timestamp gone", lines[0])
	}
	if got := lines[2].text; got != "go test ./..." {
		t.Errorf("defanged colour codes survived: %q", got)
	}
	last := lines[len(lines)-1].text
	if strings.ContainsAny(last, "\x1b\x07\r") || !strings.Contains(last, "done") {
		t.Errorf("control sequences must not reach the terminal: %q", last)
	}
	if n := countSteps(lines)["Run go test"]; n != 5 {
		t.Errorf("Run go test printed %d lines, want 5: four plus the extra, not the endgroup marker", n)
	}
}

// Enter on a step shows its log in the pane, positioned at its first error,
// and esc goes back to the list with the cursor where it was.
func TestStepLogOpensInThePane(t *testing.T) {
	v, _ := jobsView(t, run(7, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE", failingSteps...))
	deliver(v, openJobs(t, v))
	pressKeys(v, "j") // the failed step, listed under its job
	if got := cursorAt(t, v); got != "test/Run go test" {
		t.Fatalf("cursor on %q, want the failed step", got)
	}
	deliver(v, v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if v.logView == nil {
		t.Fatal("enter on a step should open its log in the pane")
	}
	out := ansi.Strip(v.PreviewView())
	for _, want := range []string{"test › Run go test", "4 lines", "1 error", "▸ Run go test ./...", "--- FAIL: TestThing", "Process completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("log pane missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Runner version") {
		t.Error("another step's lines leaked into this step's log")
	}
	if v.pendingJump == nil {
		t.Error("a failed step's log should scroll to its error")
	}
	if got := v.PreviewFocus(); got != "log" {
		t.Errorf("PreviewFocus = %q, want log", got)
	}

	pressKeys(v, "j", "j")
	if d, ok := v.TakePreviewScroll(); !ok || d != 2 {
		t.Errorf("j j asked the root to scroll %d, want 2", d)
	}
	if v.list.Selected().URL != "u1" {
		t.Error("scrolling the log moved the PR list")
	}

	pressKeys(v, "esc")
	if v.logView != nil || cursorAt(t, v) != "test/Run go test" || v.PreviewFocus() != "jobs" {
		t.Errorf("esc: log %v cursor %q focus %q, want back on the step in the jobs list",
			v.logView != nil, cursorAt(t, v), v.PreviewFocus())
	}
}

// Once the log has landed, each step says whether it printed anything, and
// enter on one that did not says so instead of opening an empty pane.
func TestStepsShowWhetherTheyHaveOutput(t *testing.T) {
	v, _ := jobsView(t, run(7, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE", failingSteps...))
	deliver(v, openJobs(t, v))
	deliver(v, v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})) // open the job: all steps
	out := ansi.Strip(v.PreviewView())
	for _, want := range []string{"1 line", "4 lines", ui.IconCISkipped} {
		if !strings.Contains(out, want) {
			t.Errorf("pane missing %q:\n%s", want, out)
		}
	}
	pressKeys(v, "j", "j", "j") // Set up job, Cache, Quiet
	if got := cursorAt(t, v); got != "test/Quiet" {
		t.Fatalf("cursor on %q, want Quiet", got)
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if v.logView != nil || !strings.Contains(ansi.Strip(v.flash), "printed nothing") {
		t.Errorf("enter on an empty step: log open %v, flash %q", v.logView != nil, ansi.Strip(v.flash))
	}
	pressKeys(v, "k")
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if v.logView != nil || !strings.Contains(ansi.Strip(v.flash), "skipped") {
		t.Errorf("enter on a skipped step: log open %v, flash %q", v.logView != nil, ansi.Strip(v.flash))
	}
}

func TestLogErrorJumpsWrap(t *testing.T) {
	v, _ := jobsView(t, run(7, 9, "CI", "pull_request", "test", "COMPLETED", "FAILURE", failingSteps...))
	deliver(v, openJobs(t, v))
	pressKeys(v, "j")
	deliver(v, v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))
	v.pendingJump = nil
	pressKeys(v, "]")
	if v.pendingJump == nil || v.logView.errIdx != 0 {
		t.Errorf("] with one error should jump back to it (idx %d)", v.logView.errIdx)
	}
	pressKeys(v, "G")
	if v.pendingJump == nil || *v.pendingJump < 1000 {
		t.Error("G should jump to the end")
	}
}

func TestExternalCheckMarksItOpensInTheBrowser(t *testing.T) {
	c := status("ci/circleci: build", "SUCCESS", "passed")
	c.TargetURL = "https://circleci.com/x"
	v, _ := jobsView(t, c)
	openJobs(t, v)
	if out := ansi.Strip(v.PreviewView()); !strings.Contains(out, "↗") {
		t.Errorf("an external check should be marked as a link:\n%s", out)
	}
}

func TestSkippedJobHasItsIcon(t *testing.T) {
	if got := jobGlyph(bucketSkipped); !strings.Contains(got, ui.IconCISkipped) {
		t.Errorf("skipped glyph = %q, want the skip icon", ansi.Strip(got))
	}
}

// A transient 5xx is retried before it is reported, and reported in words
// rather than as the JSON parse error gh produces from an HTML page.
func TestTransientErrorsRetryThenExplain(t *testing.T) {
	v, calls := jobsView(t)
	_ = v
	attempts := 0
	ghOutput = func(args ...string) ([]byte, error) {
		attempts++
		*calls = append(*calls, args)
		return nil, errors.New("invalid character '<' looking for beginning of value")
	}
	_, err := ghRetrying("api", "graphql")
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if err == nil || !strings.Contains(err.Error(), "GitHub returned an error page") {
		t.Errorf("err = %v, want it explained", err)
	}

	attempts = 0
	ghOutput = func(args ...string) ([]byte, error) {
		attempts++
		return nil, fmt.Errorf("GraphQL: Could not resolve to a Repository")
	}
	if _, err := ghRetrying("api", "graphql"); attempts != 1 || err == nil {
		t.Errorf("a real error should not be retried: attempts %d, err %v", attempts, err)
	}
}

func TestErrorPaneSaysHowToRetry(t *testing.T) {
	v, _ := jobsView(t)
	v.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	v.jobs["u1"].failures = jobsRetries + 1
	v.Update(jobsMsg{url: "u1", err: errors.New("GitHub returned an error page (a transient 5xx)")})
	out := ansi.Strip(v.PreviewView())
	if !strings.Contains(out, "error page") || !strings.Contains(out, "ctrl+r to retry") {
		t.Errorf("error pane should say what went wrong and how to retry:\n%s", out)
	}
	if cmd := v.Init(); cmd == nil || !v.jobs["u1"].inFlight {
		t.Error("ctrl+r should refetch the jobs pane")
	}
}

// The pane is not hidden when the preview pane is off: it floats over the
// list, so it keeps the keys. Showing the preview beside the list is what
// hands them back, since there are then two cursors and only one can move.
func TestShowingThePaneReturnsTheKeys(t *testing.T) {
	v, _ := jobsView(t, run(1, 9, "CI", "pull_request", "a", "COMPLETED", "SUCCESS"))
	openJobs(t, v)

	v.Update(ui.PreviewShownMsg(false))
	if v.PreviewFocus() == "" {
		t.Error("a floated jobs pane gave up the keys")
	}
	v.Update(ui.PreviewShownMsg(true))
	if v.PreviewFocus() != "" {
		t.Error("the pane kept the keys beside a visible list")
	}
}
