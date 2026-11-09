package prs

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// rateBackoff is the pause when gh cannot say when the quota returns, as
// with the secondary limit, which rate_limit does not report.
const rateBackoff = 5 * time.Minute

// Package variables so tests control the clock and stand in for gh.
var (
	ghNow   = time.Now
	ghReset = func() (time.Time, error) {
		out, err := exec.Command("gh", "api", "rate_limit", "--jq",
			`.resources | to_entries[] | select(.value.remaining == 0) | .value.reset`).Output()
		if err != nil {
			return time.Time{}, err
		}
		// Every exhausted bucket (core, graphql) must be back, so wait for the last.
		var last int64
		for _, f := range strings.Fields(string(out)) {
			if sec, perr := strconv.ParseInt(f, 10, 64); perr == nil && sec > last {
				last = sec
			}
		}
		if last == 0 {
			return time.Time{}, errors.New("no exhausted bucket")
		}
		return time.Unix(last, 0), nil
	}
)

// limit is shared by the PRs and Reviews tabs: they spend one GitHub quota,
// so one tab hitting the limit pauses both and the message is posted once.
var limit struct {
	mu    sync.Mutex
	until time.Time
}

func isRateLimit(s string) bool {
	s = strings.ToLower(s)
	for _, m := range []string{"rate limit exceeded", "secondary rate limit", "submitted too quickly"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func rateLimitedUntil() (time.Time, bool) {
	limit.mu.Lock()
	defer limit.mu.Unlock()
	return limit.until, ghNow().Before(limit.until)
}

// rateLimitErr is the one error a limited view reports. first is true only
// for the call that started the pause, so only that one posts a message.
type rateLimitErr struct {
	until time.Time
	first bool
}

func (e *rateLimitErr) Error() string {
	return "GitHub rate limited, resumes " + e.until.Local().Format("15:04")
}

// tripLimit pauses until the exhausted buckets reset. The error text does not
// say which one it was, so the quota is asked. A second failure from a call
// already in flight when the pause began finds it set and is not first.
func tripLimit() *rateLimitErr {
	if until, ok := rateLimitedUntil(); ok {
		return &rateLimitErr{until: until}
	}
	until, err := ghReset()
	if err != nil || !until.After(ghNow()) {
		until = ghNow().Add(rateBackoff)
	}
	limit.mu.Lock()
	defer limit.mu.Unlock()
	if ghNow().Before(limit.until) {
		return &rateLimitErr{until: limit.until}
	}
	limit.until = until
	return &rateLimitErr{until: until, first: true}
}

// cmdErrOut is cmdErr for a call whose stderr arrived in out.
func cmdErrOut(err error, out []byte) error {
	if isRateLimit(string(out)) {
		return tripLimit()
	}
	return cmdErr(err)
}

func asRateLimit(err error) *rateLimitErr {
	var rl *rateLimitErr
	if errors.As(err, &rl) {
		return rl
	}
	return nil
}

func (e *rateLimitErr) post() tea.Cmd {
	if !e.first {
		return nil
	}
	return statusCmd(ui.SeverityWarn, e)
}

// Paused lets the refresh timer skip this view until the limit lifts.
func (v *View) Paused() bool {
	_, ok := rateLimitedUntil()
	return ok
}

// pausedNotice answers a manual refresh during the pause without calling gh.
func pausedNotice() tea.Cmd {
	until, ok := rateLimitedUntil()
	if !ok {
		return nil
	}
	return statusCmd(ui.SeverityWarn, fmt.Errorf("GitHub still rate limited, resumes %s", until.Local().Format("15:04")))
}
