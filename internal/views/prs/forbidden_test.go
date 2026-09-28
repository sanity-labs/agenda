package prs

import (
	"errors"
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The payload GitHub actually returns when a token may read some results but
// not others: HTTP 200, readable rows, nulls for the rest, and one error per
// hidden row. Dropping the envelope loses the reason they vanished.
const partialForbidden = `{
  "data": {"search": {"nodes": [
    {"number": 7, "title": "visible", "url": "https://github.com/o/r/pull/7"},
    null,
    null
  ]}},
  "errors": [
    {"type": "FORBIDDEN", "message": "` + "`org`" + ` forbids access via a personal access token (classic). Please use a GitHub App, OAuth App, or a personal access token with fine-grained permissions."},
    {"type": "FORBIDDEN", "message": "` + "`org`" + ` forbids access via a personal access token (classic). Please use a GitHub App, OAuth App, or a personal access token with fine-grained permissions."}
  ]
}`

func TestDecodeSearchKeepsRowsAndReason(t *testing.T) {
	page, err, ok := decodeSearch([]byte(partialForbidden))
	prs := page.prs
	if !ok {
		t.Fatal("decodeSearch did not recognise the envelope")
	}
	if len(prs) != 1 || prs[0].Number != 7 {
		t.Errorf("kept %d PRs %v, want just #7", len(prs), prs)
	}
	if err == nil {
		t.Fatal("no error for a partly forbidden search: the hidden rows would vanish silently")
	}
	if !strings.Contains(err.Error(), "2 of 3 hidden") {
		t.Errorf("error = %q, want it to count the hidden rows", err)
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error = %q, want the remediation hint", err)
	}
}

func TestDecodeSearchCleanResponse(t *testing.T) {
	page, err, ok := decodeSearch([]byte(`{"data":{"search":{"nodes":[{"number":1},{"number":2}]}}}`))
	prs := page.prs
	if !ok || err != nil {
		t.Fatalf("clean response: ok=%v err=%v, want true/nil", ok, err)
	}
	if len(prs) != 2 {
		t.Errorf("kept %d PRs, want 2", len(prs))
	}
}

func TestDecodeSearchRejectsNonEnvelope(t *testing.T) {
	if _, _, ok := decodeSearch([]byte("<html>gateway timeout</html>")); ok {
		t.Error("decodeSearch accepted an HTML error page")
	}
}

// Every row forbidden is a total failure, not a partial one.
func TestDecodeSearchAllForbidden(t *testing.T) {
	body := `{"data":{"search":{"nodes":[null,null]}},"errors":[{"message":"nope"},{"message":"nope"}]}`
	page, err, ok := decodeSearch([]byte(body))
	prs := page.prs
	if !ok {
		t.Fatal("not recognised")
	}
	if len(prs) != 0 {
		t.Errorf("kept %d PRs, want none", len(prs))
	}
	if err == nil || strings.Contains(err.Error(), "of") {
		t.Errorf("error = %v, want the bare message with no partial count", err)
	}
}

// A failed refresh with cached rows on screen must warn, not blank the view:
// the old rows are still useful, they are just stale.
func TestFailedRefreshOverCacheWarnsInsteadOfBlanking(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.raw = []pr{{Number: 1, Title: "cached"}}
	v.applySort()

	cmd := v.Update(mineMsg{err: errors.New("network unreachable")})
	if v.err != nil {
		t.Errorf("view error set to %v, want the cached rows kept without an error", v.err)
	}
	if len(v.raw) != 1 {
		t.Errorf("cached rows dropped: %d left, want 1", len(v.raw))
	}
	if cmd == nil {
		t.Fatal("no status message raised: a failed refresh would be silent")
	}
	msg, ok := cmd().(ui.StatusMsg)
	if !ok {
		t.Fatalf("raised %T, want ui.StatusMsg", cmd())
	}
	if msg.Severity != ui.SeverityWarn || !strings.Contains(msg.Summary, "cached") {
		t.Errorf("message = %+v, want a warning about cached data", msg)
	}
}

// With no data at all, a failed fetch is still a hard error. New() seeds
// from the on-disk cache, so clear it to test the empty case.
func TestFailedFetchWithNoCacheSetsError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.raw = nil
	v.Update(mineMsg{err: errors.New("network unreachable")})
	if v.err == nil {
		t.Error("no error set with nothing to show, want the failure surfaced")
	}
}
