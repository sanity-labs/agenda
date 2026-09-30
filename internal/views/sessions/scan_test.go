package sessions

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sanity-labs/agenda/internal/config"
)

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"a  b\n c":   "a b c",
		"  trim  ":   "trim",
		"one\t\ttwo": "one two",
		"":           "",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsRealUserText(t *testing.T) {
	real := []string{"fix the bug", "what does this do?"}
	noise := []string{
		"",
		"   ",
		"<system-reminder>do x</system-reminder>",
		`{"tool_use_id": "abc"}`,
		"Caveat: the messages below were generated…",
		"[Request interrupted by user]",
	}
	for _, s := range real {
		if !isRealUserText(s) {
			t.Errorf("isRealUserText(%q) = false, want true", s)
		}
	}
	for _, s := range noise {
		if isRealUserText(s) {
			t.Errorf("isRealUserText(%q) = true, want false", s)
		}
	}
}

func TestTextFromContent(t *testing.T) {
	// String form.
	if got := textFromContent(json.RawMessage(`"hello"`)); got != "hello" {
		t.Errorf("string content = %q, want hello", got)
	}
	// Array of typed blocks; only text/input_text contribute.
	arr := json.RawMessage(`[{"type":"text","text":"a"},{"type":"image","text":"skip"},{"type":"input_text","text":"b"}]`)
	if got := textFromContent(arr); got != "a b" {
		t.Errorf("array content = %q, want %q", got, "a b")
	}
	if got := textFromContent(nil); got != "" {
		t.Errorf("nil content = %q, want empty", got)
	}
}

func TestShortenPath(t *testing.T) {
	h := home()
	cases := map[string]string{
		"":         "?",
		h:          "~",
		h + "/x/y": "~/x/y",
		"/other":   "/other",
	}
	for in, want := range cases {
		if got := shortenPath(in); got != want {
			t.Errorf("shortenPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseClaude(t *testing.T) {
	dir := t.TempDir()
	// ai-title present -> wins over user prompts; tool/reminder lines ignored.
	content := `{"type":"user","cwd":"/home/u/proj","message":{"content":"first prompt"}}
{"type":"assistant","message":{"content":"hi"}}
{"type":"user","message":{"content":"<system-reminder>ignore me</system-reminder>"}}
{"type":"user","message":{"content":"second prompt"}}
{"type":"ai-title","aiTitle":"Generated Title"}
`
	p := writeFile(t, dir, "abc123.jsonl", content)
	m := parseClaude(p)
	if m.Cwd != "/home/u/proj" {
		t.Errorf("Cwd = %q, want /home/u/proj", m.Cwd)
	}
	if m.Msgs != 2 {
		t.Errorf("Msgs = %d, want 2 (reminder line excluded)", m.Msgs)
	}
	if m.Title != "Generated Title" {
		t.Errorf("Title = %q, want ai-title to win", m.Title)
	}
	if m.SessionID != "abc123" {
		t.Errorf("SessionID = %q, want filename stem abc123", m.SessionID)
	}
}

func TestParseClaudeCustomTitleWins(t *testing.T) {
	dir := t.TempDir()
	// A renamed session: /rename writes a custom-title record. It must override
	// the ai-title (which claude -r ignores once you've renamed).
	content := `{"type":"user","cwd":"/home/u/proj","message":{"content":"first prompt"}}
{"type":"ai-title","aiTitle":"Generated Title"}
{"type":"custom-title","customTitle":"my renamed session","sessionId":"abc123"}
`
	m := parseClaude(writeFile(t, dir, "abc123.jsonl", content))
	if m.Title != "my renamed session" {
		t.Errorf("Title = %q, want custom-title to override ai-title", m.Title)
	}
}

func TestParseClaudeLastCustomTitleWins(t *testing.T) {
	dir := t.TempDir()
	// custom-title is re-appended on each load and can change on a later rename;
	// the most recent one wins.
	content := `{"type":"custom-title","customTitle":"old name"}
{"type":"ai-title","aiTitle":"Generated Title"}
{"type":"custom-title","customTitle":"new name"}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	if m.Title != "new name" {
		t.Errorf("Title = %q, want last custom-title", m.Title)
	}
}

func TestParseClaudeFallsBackToAiTitleWhenNoCustom(t *testing.T) {
	dir := t.TempDir()
	// No custom-title -> ai-title still wins (unchanged behavior).
	content := `{"type":"user","message":{"content":"a prompt"}}
{"type":"ai-title","aiTitle":"Generated Title"}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	if m.Title != "Generated Title" {
		t.Errorf("Title = %q, want ai-title when no custom-title", m.Title)
	}
}

func TestParseClaudeFallsBackToLastPrompt(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"user","message":{"content":"only prompt"}}
{"type":"user","message":{"content":"latest prompt"}}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	if m.Title != "latest prompt" {
		t.Errorf("Title = %q, want last user prompt when no ai-title", m.Title)
	}
}

func TestParseClaudeFallsBackToFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	// A single user prompt and no titles: first == last, so the first-prompt
	// fallback is what surfaces.
	content := `{"type":"user","message":{"content":"only prompt"}}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	if m.Title != "only prompt" {
		t.Errorf("Title = %q, want the sole user prompt", m.Title)
	}
}

func TestParseClaudeComputesCostAndModel(t *testing.T) {
	dir := t.TempDir()
	// Two assistant messages carry usage + model. Opus 4.x rates ($5/$25 in/out,
	// $0.625 cache-read, $6.25 cache-write per 1M).
	// Totals: in 2_000_000, out 400_000, cacheRead 8_000_000, cacheWrite 1_000_000
	// cost = 2*5 + 0.4*25 + 8*0.625 + 1*6.25 = 10 + 10 + 5 + 6.25 = 31.25
	content := `{"type":"user","cwd":"/home/u/proj","entrypoint":"cli","message":{"content":"hi"}}
{"type":"assistant","message":{"model":"claude-opus-4-8","content":"a","usage":{"input_tokens":1000000,"output_tokens":200000,"cache_read_input_tokens":4000000,"cache_creation_input_tokens":500000}}}
{"type":"assistant","message":{"model":"claude-opus-4-8","content":"b","usage":{"input_tokens":1000000,"output_tokens":200000,"cache_read_input_tokens":4000000,"cache_creation_input_tokens":500000}}}
`
	m := parseClaude(writeFile(t, dir, "abc123.jsonl", content))
	if m.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8", m.Model)
	}
	if math.Abs(m.Cost-31.25) > 1e-6 {
		t.Errorf("Cost = %v, want 31.25", m.Cost)
	}
	if m.Entrypoint != "cli" {
		t.Errorf("Entrypoint = %q, want cli", m.Entrypoint)
	}
}

func TestParseClaudeCapturesSDKEntrypoint(t *testing.T) {
	dir := t.TempDir()
	// Programmatic sessions carry entrypoint "sdk-py" (security-review, /code-review, etc.).
	content := `{"type":"queue-operation","entrypoint":"sdk-py","content":"Review this change for security vulnerabilities."}
{"type":"user","message":{"content":"Review this change for security vulnerabilities."}}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	if m.Entrypoint != "sdk-py" {
		t.Errorf("Entrypoint = %q, want sdk-py", m.Entrypoint)
	}
}

func TestIsAgent(t *testing.T) {
	cases := map[string]bool{
		"cli":    false, // interactive
		"sdk-py": true,  // programmatic
		"":       false, // non-Claude tools (no entrypoint)
	}
	for ep, want := range cases {
		s := session{meta: meta{Entrypoint: ep}}
		if got := s.isAgent(); got != want {
			t.Errorf("isAgent(entrypoint=%q) = %v, want %v", ep, got, want)
		}
	}
}

func TestApplyViewHidesAgents(t *testing.T) {
	v := New(config.SessionsConfig{}, nil, nil)
	v.raw = []session{
		{meta: meta{SessionID: "you1", Entrypoint: "cli", Cost: 5}, Tool: toolClaude, Updated: time.Now()},
		{meta: meta{SessionID: "agent1", Entrypoint: "sdk-py", Cost: 3}, Tool: toolClaude, Updated: time.Now()},
		{meta: meta{SessionID: "codex1", Cost: 0}, Tool: toolCodex, Updated: time.Now()},
	}
	// Default: agents hidden.
	v.applyView()
	if got := v.list.Total(); got != 2 {
		t.Errorf("hidden: list total = %d, want 2 (agent excluded)", got)
	}
	// Toggle on: all shown.
	v.showAgents = true
	v.applyView()
	if got := v.list.Total(); got != 3 {
		t.Errorf("shown: list total = %d, want 3", got)
	}
}

func TestParseClaudeNoUsageZeroCost(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"user","cwd":"/home/u/proj","message":{"content":"hi"}}
{"type":"assistant","message":{"content":"no usage here"}}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	if m.Cost != 0 {
		t.Errorf("Cost = %v, want 0 when no usage", m.Cost)
	}
	if m.Model != "" {
		t.Errorf("Model = %q, want empty when no model", m.Model)
	}
}

func TestParseCodex(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"session_meta","payload":{"cwd":"/x/y","id":"sess-42"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello there"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"text","text":"hi"}]}}
`
	m := parseCodex(writeFile(t, dir, "rollout-x.jsonl", content))
	if m.Cwd != "/x/y" {
		t.Errorf("Cwd = %q, want /x/y", m.Cwd)
	}
	if m.SessionID != "sess-42" {
		t.Errorf("SessionID = %q, want sess-42 from meta", m.SessionID)
	}
	if m.Msgs != 1 {
		t.Errorf("Msgs = %d, want 1 (only user messages counted)", m.Msgs)
	}
	if m.Title != "hello there" {
		t.Errorf("Title = %q, want first user prompt", m.Title)
	}
}

func TestParseClaudeLastTS(t *testing.T) {
	dir := t.TempDir()
	// Records are append-ordered, so the newest stamped record dates the
	// session. Untimestamped records (ai-title) must not clear it.
	content := `{"type":"user","timestamp":"2026-07-01T22:45:00.000Z","message":{"content":"first"}}
{"type":"assistant","timestamp":"2026-07-02T09:46:12.500Z","message":{"content":"hi"}}
{"type":"ai-title","aiTitle":"Generated Title"}
`
	m := parseClaude(writeFile(t, dir, "s.jsonl", content))
	want := time.Date(2026, 7, 2, 9, 46, 12, 500_000_000, time.UTC)
	if !m.LastTS.Equal(want) {
		t.Errorf("LastTS = %v, want %v (last stamped record)", m.LastTS, want)
	}
}

func TestParseCodexLastTS(t *testing.T) {
	dir := t.TempDir()
	content := `{"timestamp":"2026-06-16T09:54:37.684Z","type":"session_meta","payload":{"cwd":"/x","id":"s1"}}
{"timestamp":"2026-06-16T10:02:00.000Z","type":"event_msg","payload":{"type":"task_complete"}}
`
	m := parseCodex(writeFile(t, dir, "rollout-x.jsonl", content))
	want := time.Date(2026, 6, 16, 10, 2, 0, 0, time.UTC)
	if !m.LastTS.Equal(want) {
		t.Errorf("LastTS = %v, want %v", m.LastTS, want)
	}
}

func TestParseTSInvalid(t *testing.T) {
	for _, in := range []string{"", "not a time", "2026-13-99"} {
		if got := parseTS(in); !got.IsZero() {
			t.Errorf("parseTS(%q) = %v, want zero time", in, got)
		}
	}
}

func TestUpdatedAt(t *testing.T) {
	mtime := time.Date(2026, 8, 3, 7, 58, 17, 0, time.UTC)
	// The real bug: a July session whose mtime was bumped to August by a
	// restore/migration must still be dated July.
	content := time.Date(2026, 7, 1, 23, 3, 0, 0, time.UTC)
	if got := updatedAt(meta{LastTS: content}, mtime); !got.Equal(content) {
		t.Errorf("updatedAt with LastTS = %v, want the conversation time %v", got, content)
	}
	// Formats without timestamps (Antigravity) still fall back to mtime.
	if got := updatedAt(meta{}, mtime); !got.Equal(mtime) {
		t.Errorf("updatedAt without LastTS = %v, want mtime %v", got, mtime)
	}
}

func TestConversationTurnsClaude(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"user","message":{"content":"q1"}}
{"type":"assistant","message":{"content":"a1"}}
{"type":"user","message":{"content":"<system-reminder>skip</system-reminder>"}}
{"type":"user","message":{"content":"q2"}}
`
	turns := conversationTurns(writeFile(t, dir, "s.jsonl", content), toolClaude)
	want := []turn{{"user", "q1"}, {"assistant", "a1"}, {"user", "q2"}}
	if len(turns) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(turns), len(want), turns)
	}
	for i, w := range want {
		if turns[i] != w {
			t.Errorf("turn %d = %+v, want %+v", i, turns[i], w)
		}
	}
}

func TestPreviewWindowing(t *testing.T) {
	// A session file with 20 user turns; "needle" only in turn 3.
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 20; i++ {
		txt := fmt.Sprintf("turn %d body", i)
		if i == 3 {
			txt = "turn 3 has the needle here"
		}
		lines = append(lines, fmt.Sprintf(`{"type":"user","message":{"content":%q}}`, txt))
	}
	path := writeFile(t, dir, "s.jsonl", strings.Join(lines, "\n")+"\n")
	// Body must contain the query so the row survives the list filter (as it would
	// in the real app — the body is why the session matches).
	s := session{meta: meta{SessionID: "s", Body: "turn 3 has the needle here"}, Tool: toolClaude, Path: path}

	v := New(config.SessionsConfig{}, nil, nil)
	v.list.SetItems([]session{s})
	v.list.SetEnabledFields([]string{"text"}) // scope to body so 'needle' matches it

	// No query: default window shows the last maxTurns, with an "earlier turns"
	// marker for the hidden ones (20 - 14 = 6 hidden).
	out := v.PreviewView()
	if !strings.Contains(out, "6 earlier turns") {
		t.Errorf("no-query preview missing earlier-turns marker:\n%s", out)
	}
	if strings.Contains(out, "turn 0 body") {
		t.Errorf("no-query preview should hide the oldest turn")
	}
	if !v.hasHiddenTurns {
		t.Errorf("hasHiddenTurns should be true when turns are hidden")
	}

	// Query mode: matches-only. Only turn 3 (the match) renders; the marker is
	// gone and non-matching turns are absent.
	v.list.SetQuery("needle")
	out = v.PreviewView()
	if !strings.Contains(out, "needle") {
		t.Errorf("query preview should show the matching turn:\n%s", out)
	}
	if strings.Contains(out, "earlier turns") {
		t.Errorf("query preview should be matches-only (no window marker):\n%s", out)
	}
	if strings.Contains(out, "turn 5 body") {
		t.Errorf("query preview should exclude non-matching turns")
	}
	if v.hasHiddenTurns {
		t.Errorf("hasHiddenTurns should be false in matches-only mode")
	}
}

func TestCachedTurnsMemoizes(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "s.jsonl", `{"type":"user","message":{"content":"hello"}}`+"\n")
	s := session{Tool: toolClaude, Path: path}
	v := New(config.SessionsConfig{}, nil, nil)
	first := v.cachedTurns(s)
	// Delete the file; a memoized second call must NOT re-read (would return empty).
	_ = os.Remove(path)
	second := v.cachedTurns(s)
	if len(second) != len(first) || len(second) == 0 {
		t.Errorf("cachedTurns re-read after file removal: first=%d second=%d", len(first), len(second))
	}
	// A different path invalidates the cache.
	other := session{Tool: toolClaude, Path: filepath.Join(dir, "gone.jsonl")}
	if got := v.cachedTurns(other); got != nil {
		t.Errorf("cachedTurns for a new path should re-parse (got %d turns)", len(got))
	}
}

func TestScanMentions(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"user","message":{"content":"working on SRE-4419 today"}}
{"type":"assistant","message":{"content":"see https://github.com/sanity-io/argocd-apps/pull/7314 for the change"}}
{"type":"user","message":{"content":"SRE-4419 again, should be deduped"}}
`
	ms := scanMentions(writeFile(t, dir, "s.jsonl", content), toolClaude)

	var linear, pr *mention
	for i := range ms {
		switch ms[i].Kind {
		case "linear":
			linear = &ms[i]
		case "pr":
			pr = &ms[i]
		}
	}
	if linear == nil || linear.ID != "SRE-4419" {
		t.Errorf("linear mention = %+v, want SRE-4419", linear)
	}
	if linear != nil && linear.Snippet == "" {
		t.Error("linear mention has no context snippet")
	}
	if pr == nil || pr.ID != "https://github.com/sanity-io/argocd-apps/pull/7314" {
		t.Errorf("pr mention = %+v, want the PR URL", pr)
	}
	// SRE-4419 appears twice but should be recorded once.
	count := 0
	for _, m := range ms {
		if m.ID == "SRE-4419" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("SRE-4419 recorded %d times, want 1 (deduped)", count)
	}
}

func TestSortSessions(t *testing.T) {
	base := time.Now()
	in := []session{
		{meta: meta{Cwd: "/b", Msgs: 1, Cost: 12.50}, Tool: toolCodex, Updated: base.Add(-2 * time.Hour)},
		{meta: meta{Cwd: "/a", Msgs: 9, Cost: 0.30}, Tool: toolClaude, Updated: base.Add(-1 * time.Hour)},
		{meta: meta{Cwd: "/c", Msgs: 5, Cost: 3.75}, Tool: toolClaude, Updated: base.Add(-3 * time.Hour)},
	}

	recent := sortSessions(in, sortRecent, false)
	if !recent[0].Updated.After(recent[1].Updated) || !recent[1].Updated.After(recent[2].Updated) {
		t.Error("sortRecent not newest-first")
	}
	msgs := sortSessions(in, sortMsgs, false)
	if msgs[0].Msgs != 9 {
		t.Errorf("sortMsgs[0].Msgs = %d, want 9 (highest first)", msgs[0].Msgs)
	}
	byCwd := sortSessions(in, sortCwd, false)
	if byCwd[0].Cwd != "/a" || byCwd[2].Cwd != "/c" {
		t.Errorf("sortCwd order = %q..%q, want /a../c", byCwd[0].Cwd, byCwd[2].Cwd)
	}
	cost := sortSessions(in, sortCost, false)
	if cost[0].Cost != 12.50 || cost[2].Cost != 0.30 {
		t.Errorf("sortCost order = %.2f..%.2f, want 12.50..0.30 (highest first)", cost[0].Cost, cost[2].Cost)
	}
	// Original slice must be untouched (sortSessions copies).
	if in[0].Cwd != "/b" {
		t.Error("sortSessions mutated its input")
	}
}

// Reversing a mode must invert it exactly — this is what replaced the old
// dedicated "oldest" mode.
func TestSortSessionsReversed(t *testing.T) {
	base := time.Now()
	in := []session{
		{meta: meta{Cwd: "/b", Msgs: 1}, Tool: toolCodex, Updated: base.Add(-2 * time.Hour)},
		{meta: meta{Cwd: "/a", Msgs: 9}, Tool: toolClaude, Updated: base.Add(-1 * time.Hour)},
		{meta: meta{Cwd: "/c", Msgs: 5}, Tool: toolClaude, Updated: base.Add(-3 * time.Hour)},
	}

	oldest := sortSessions(in, sortRecent, true)
	if !oldest[0].Updated.Before(oldest[2].Updated) {
		t.Error("reversed sortRecent not oldest-first")
	}
	msgs := sortSessions(in, sortMsgs, true)
	if msgs[0].Msgs != 1 {
		t.Errorf("reversed sortMsgs[0].Msgs = %d, want 1 (lowest first)", msgs[0].Msgs)
	}
	byCwd := sortSessions(in, sortCwd, true)
	if byCwd[0].Cwd != "/c" || byCwd[2].Cwd != "/a" {
		t.Errorf("reversed sortCwd order = %q..%q, want /c../a", byCwd[0].Cwd, byCwd[2].Cwd)
	}
}
