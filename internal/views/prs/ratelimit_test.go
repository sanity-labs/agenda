package prs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

const limitText = "gh: API rate limit exceeded for user ID 2885448. If you reach out to GitHub Support (HTTP 403)"

// limitedView pins the clock and the reset lookup, and clears the shared
// pause so tests do not leak into each other.
func limitedView(t *testing.T, now *time.Time, reset time.Time) *View {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no gh: any call to it fails loudly
	oldNow, oldReset := ghNow, ghReset
	ghNow = func() time.Time { return *now }
	ghReset = func() (time.Time, error) { return reset, nil }
	limit.until = time.Time{}
	t.Cleanup(func() { ghNow, ghReset, limit.until = oldNow, oldReset, time.Time{} })
	return New(config.GitHubConfig{}, nil, nil, nil)
}

func statusOf(t *testing.T, cmd tea.Cmd) (ui.StatusMsg, bool) {
	t.Helper()
	if cmd == nil {
		return ui.StatusMsg{}, false
	}
	msg, ok := cmd().(ui.StatusMsg)
	return msg, ok
}

func TestIsRateLimit(t *testing.T) {
	for _, s := range []string{
		limitText,
		"API rate limit exceeded",
		"You have exceeded a secondary rate limit.",
		"was submitted too quickly",
	} {
		if !isRateLimit(s) {
			t.Errorf("%q not detected", s)
		}
	}
	for _, s := range []string{"HTTP 404", "not mergeable", "invalid character '<'"} {
		if isRateLimit(s) {
			t.Errorf("%q wrongly detected", s)
		}
	}
}

func TestRateLimitPostsOneLineAndPausesBothTabs(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	reset := time.Date(2026, 11, 9, 16, 3, 0, 0, time.Local)
	mine := limitedView(t, &now, reset)
	reviews := New(config.GitHubConfig{}, nil, nil, nil)

	first := tripLimit()
	second := tripLimit()
	if !first.first || second.first {
		t.Fatalf("first flags = %v, %v; want true, false", first.first, second.first)
	}

	msg, ok := statusOf(t, mine.Update(mineMsg{from: mine, err: first}))
	if !ok || msg.Summary != "GitHub rate limited, resumes 16:03" || msg.Detail != "" {
		t.Fatalf("status = %+v, want the one short line", msg)
	}
	if _, ok := statusOf(t, mine.Update(mineMsg{from: mine, err: second})); ok {
		t.Error("a second failure posted the message again")
	}
	if _, ok := statusOf(t, reviews.Update(reviewListMsg{from: reviews, err: second})); ok {
		t.Error("the other tab posted the message again")
	}
	if !mine.Paused() || !reviews.Paused() {
		t.Error("both tabs should be paused")
	}
	if strings.Contains(mine.err.Error(), "HTTP 403") {
		t.Errorf("raw gh text leaked into the view: %v", mine.err)
	}
}

func TestSearchSkipsGhWhilePausedAndResumesAfterReset(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	reset := now.Add(time.Hour)
	limitedView(t, &now, reset)
	_ = tripLimit()

	_, err, _ := searchPRs("is:pr", 10, "")
	if rl := asRateLimit(err); rl == nil || rl.first {
		t.Fatalf("paused search err = %v, want a quiet rate-limit error without calling gh", err)
	}
	now = reset.Add(time.Second)
	if _, err, _ := searchPRs("is:pr", 10, ""); asRateLimit(err) != nil {
		t.Fatalf("still paused after the reset: %v", err)
	}
}

func TestManualRefreshWhilePausedSaysSo(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	v := limitedView(t, &now, time.Date(2026, 11, 9, 16, 3, 0, 0, time.Local))
	_ = tripLimit()
	v.loading = false

	msg, ok := statusOf(t, v.Init())
	if !ok || msg.Summary != "GitHub still rate limited, resumes 16:03" {
		t.Fatalf("manual refresh status = %+v", msg)
	}
	if v.loading {
		t.Error("a paused refresh started the spinner")
	}
}

func TestBackoffWhenResetUnknown(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	limitedView(t, &now, time.Time{})
	ghReset = func() (time.Time, error) { return time.Time{}, errors.New("offline") }
	rl := tripLimit()
	if want := now.Add(rateBackoff); !rl.until.Equal(want) {
		t.Errorf("until = %v, want %v", rl.until, want)
	}
}

// fakeGh makes every gh call fail the way a limited account does, on stderr.
func fakeGh(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + limitText + "' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// REST and subcommand calls fail with the same short line, and the
// failure starts the pause that automatic polling honours.
func TestRESTAndActionErrorsBecomeTheShortLine(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	reset := time.Date(2026, 11, 9, 16, 3, 0, 0, time.Local)
	limitedView(t, &now, reset)
	fakeGh(t)
	want := "GitHub rate limited, resumes 16:03"

	files := fetchFiles("u", "o/r", 1)().(filesMsg)
	if files.err == nil || files.err.Error() != want {
		t.Errorf("files err = %v, want %q", files.err, want)
	}
	if _, ok := rateLimitedUntil(); !ok {
		t.Error("a REST failure did not start the pause")
	}

	limit.until = time.Time{}
	done := submitTopComment(nil, "u", "o/r", 1, "hi")().(threadDoneMsg)
	if done.err == nil || done.err.Error() != want {
		t.Errorf("comment err = %v, want %q", done.err, want)
	}

	limit.until = time.Time{}
	if err := ghErr(errors.New("exit status 1"), []byte(limitText)); err.Error() != want {
		t.Errorf("ghErr = %v, want %q", err, want)
	}
}

func TestPollingIsSkippedWhilePaused(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	v := limitedView(t, &now, now.Add(time.Hour))
	_ = tripLimit()
	if fetchReadThreads() != nil {
		t.Error("notification sync ran while paused")
	}
	v.pane = paneJobs
	v.Update(mineMsg{from: v, page: searchPage{prs: []pr{{Number: 1, URL: "u", Title: "t"}}}})
	if v.maybeFetchJobs() != nil {
		t.Error("jobs polling ran while paused")
	}
}

func TestPaneShowsPausedStateNotError(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	v := limitedView(t, &now, time.Date(2026, 11, 9, 16, 3, 0, 0, time.Local))
	v.loading = false
	rl := tripLimit()
	v.Update(mineMsg{from: v, err: rl})
	if got := v.statusText(); got != "GitHub rate limited, resumes 16:03" {
		t.Errorf("statusText = %q", got)
	}

	// A list that was already loaded stays, and the line still says why it is stale.
	v.Update(mineMsg{from: v, page: searchPage{prs: []pr{{Number: 1, URL: "u", Title: "t"}}}})
	v.Update(mineMsg{from: v, err: rl})
	if len(v.raw) != 1 || v.statusText() != "GitHub rate limited, resumes 16:03" {
		t.Errorf("raw = %d, statusText = %q", len(v.raw), v.statusText())
	}
}

func TestNotificationSyncPostsTheRateLimitNotice(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.Local)
	limitedView(t, &now, time.Date(2026, 11, 9, 16, 3, 0, 0, time.Local))
	resetNotifSync()
	t.Cleanup(resetNotifSync)
	old := ghRun
	t.Cleanup(func() { ghRun = old })
	ghRun = func(args ...string) ([]byte, error) {
		return []byte(limitText), errors.New("exit status 1")
	}
	msg, ok := fetchReadThreads()().(ui.StatusMsg)
	if !ok || msg.Summary != "GitHub rate limited, resumes 16:03" {
		t.Errorf("sync msg = %#v", msg)
	}
}
