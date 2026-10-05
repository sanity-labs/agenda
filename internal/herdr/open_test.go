package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeRun answers commands from canned stdout, keyed by the command line
// ("herdr workspace list", "git fetch --quiet origin feat"). Commands in fail
// exit non-zero; anything else succeeds with an empty result.
type fakeRun struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
}

func newFake() *fakeRun {
	return &fakeRun{out: map[string]string{
		"herdr workspace list": `{"result":{"workspaces":[]}}`,
		"herdr agent list":     `{"result":{"agents":[]}}`,
		"herdr pane list":      `{"result":{"panes":[]}}`,
	}, fail: map[string]bool{}}
}

func (f *fakeRun) run(_, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, cmd)
	if f.fail[cmd] {
		return nil, errors.New("exit status 1")
	}
	if out, ok := f.out[cmd]; ok {
		return []byte(out), nil
	}
	return []byte(`{"result":{}}`), nil
}

// called reports whether a command starting with prefix ran.
func (f *fakeRun) called(prefix string) bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

// mkRepo creates a directory that passes for a git checkout.
func mkRepo(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type testEnv struct {
	o      *Opener
	f      *fakeRun
	root   string // repos root
	wtDir  string // worktrees directory
	claims *Claims
}

func newEnv(t *testing.T) testEnv {
	t.Helper()
	t.Setenv("HERDR_BIN_PATH", "herdr")
	t.Setenv("HERDR_ACTIVE_WORKSPACE_ID", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	f := newFake()
	root := t.TempDir()
	wt := filepath.Join(root, ".worktrees")
	c := LoadClaims(filepath.Join(t.TempDir(), "claims.json"))
	return testEnv{o: NewOpener(f.run, Repos{Root: root}, wt, c), f: f, root: root, wtDir: wt, claims: c}
}

func (e testEnv) workspaces(json string) {
	e.f.out["herdr workspace list"] = `{"result":{"workspaces":[` + json + `]}}`
}

// grafana is a sub-issue in a project, the shape most issues have.
var grafana = Issue{
	ID: "SRE-5170", Title: "Bump the chart", Branch: "sre-5170-bump",
	Parent: "SRE-4000", ParentTitle: "Grafana 13", ProjectID: "p-graf", Project: "Grafana 13 Upgrade",
}

func TestUnclaimedIssueAsksWhere(t *testing.T) {
	e := newEnv(t)
	t.Setenv("HERDR_ACTIVE_WORKSPACE_ID", "wK")
	e.workspaces(`{"workspace_id":"wK","label":"[3] KTLO"},{"workspace_id":"wZ","label":"SRE-51700 other"}`)

	err := e.o.Open(Request{Issues: []Issue{grafana}})
	var ce *ChoiceError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a ChoiceError (SRE-51700 is a different issue)", err)
	}
	var got []string
	for _, c := range ce.Choices {
		got = append(got, c.Label)
	}
	want := []string{
		"New workspace for SRE-5170 Bump the chart",
		"New workspace for parent SRE-4000 Grafana 13",
		"New workspace for project Grafana 13 Upgrade",
		"Add SRE-5170 to KTLO",
		"Add SRE-4000 and its sub-issues to KTLO",
		"Add project Grafana 13 Upgrade to KTLO",
	}
	if !slices.Equal(got, want) {
		t.Errorf("choices =\n%q\nwant\n%q", got, want)
	}
}

func TestNewProjectWorkspaceStartsTheIssueBranch(t *testing.T) {
	e := newEnv(t)
	clone := mkRepo(t, filepath.Join(e.root, "sanity-io", "ops"))
	e.f.out["git worktree list --porcelain"] = "worktree " + clone + "\nbranch refs/heads/main\n"
	e.f.fail["git rev-parse --verify --quiet refs/heads/sre-5170-bump"] = true
	e.f.out["git symbolic-ref --quiet --short refs/remotes/origin/HEAD"] = "origin/main\n"
	path := filepath.Join(e.wtDir, "ops", "sre-5170-bump")
	e.f.out["herdr workspace create --cwd "+path+" --label Grafana 13 Upgrade --focus"] =
		`{"result":{"workspace":{"workspace_id":"w9","label":"Grafana 13 Upgrade"},"tab":{"tab_id":"w9:t1"}}}`

	choice := Choice{Node: Node{"project", "p-graf"}, NewLabel: "Grafana 13 Upgrade"}
	if err := e.o.Open(Request{Issues: []Issue{grafana}, Claim: &choice, Clone: clone}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"git fetch --quiet origin main",
		"git worktree add --no-track -b sre-5170-bump " + path + " origin/main",
		"herdr tab rename w9:t1 SRE-5170 · ops",
	} {
		if !e.f.called(want) {
			t.Errorf("calls = %q\nwant %q", e.f.calls, want)
		}
	}
	// The project is claimed now: its other issues open in w9 without asking.
	if ws := e.claims.Owner(Node{"project", "p-graf"}, []Workspace{{ID: "w9", Label: "[1] Grafana 13 Upgrade"}}); ws != "w9" {
		t.Errorf("project owner = %q, want w9", ws)
	}
}

func TestIssueInClaimedProjectFocusesItsTab(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"w9","label":"Grafana upgrade"}`)
	_ = e.claims.Add("w9", "Grafana upgrade", Node{"project", "p-graf"})
	e.f.out["herdr tab list --workspace w9"] = `{"result":{"tabs":[
		{"tab_id":"w9:t1","label":"SRE-4329 · ops"},{"tab_id":"w9:t2","label":"SRE-5170 · ops"}]}}`

	if err := e.o.Open(Request{Issues: []Issue{grafana}}); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr workspace focus w9") || !e.f.called("herdr tab focus w9:t2") {
		t.Errorf("calls = %q, want w9 and SRE-5170's tab focused", e.f.calls)
	}
}

func TestIssueInClaimedWorkspaceAsksForRepoOrJustOpens(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"w9","label":"Grafana upgrade"}`)
	_ = e.claims.Add("w9", "Grafana upgrade", Node{"project", "p-graf"})

	var re *RepoError
	if err := e.o.Open(Request{Issues: []Issue{grafana}}); !errors.As(err, &re) || re.Workspace != "w9" {
		t.Fatalf("err = %v, want a RepoError offering w9", err)
	}
	if err := e.o.Open(Request{Issues: []Issue{grafana}, FocusOnly: true}); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr workspace focus w9") || e.f.called("git worktree add") {
		t.Errorf("calls = %q, want w9 focused and no branch started", e.f.calls)
	}
}

func TestLabelledWorkspaceIsFoundAndClaimed(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"wB","label":"[6] SRE-2999 Enforce CloudSQL"}`)

	req := Request{Issues: []Issue{{ID: "SRE-2999"}}, FocusOnly: true}
	if err := e.o.Open(req); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr workspace focus wB") {
		t.Errorf("calls = %q, want wB focused", e.f.calls)
	}
	// Recorded, so a rename doesn't lose it.
	if ws := e.claims.Owner(Node{"issue", "SRE-2999"}, []Workspace{{ID: "wB", Label: "CloudSQL"}}); ws != "wB" {
		t.Errorf("owner after rename = %q, want wB", ws)
	}
}

func TestPRAddsTabToItsIssueWorkspace(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"wP","label":"PSC"}`)
	_ = e.claims.Add("wP", "PSC", Node{"issue", "SRE-5287"})
	clone := mkRepo(t, filepath.Join(e.root, "sanity-io", "argocd-ops"))
	// Another PR of the same issue is checked out in an agent's scratchpad.
	e.f.out["git worktree list --porcelain"] = "worktree " + clone + "\nbranch refs/heads/main\n\n" +
		"worktree /tmp/agent/ops-5287\nbranch refs/heads/sre-5287-tools-staging\n"
	e.f.fail["git rev-parse --verify --quiet refs/heads/sre-5287-timeouts"] = true
	path := filepath.Join(e.wtDir, "argocd-ops", "sre-5287-timeouts")

	req := Request{
		Issues: []Issue{{ID: "SRE-5287", Title: "PSC"}},
		PR:     &PR{Repo: "sanity-io/argocd-ops", Number: 4454, Branch: "sre-5287-timeouts"},
	}
	if err := e.o.Open(req); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"git worktree add --track -b sre-5287-timeouts " + path + " origin/sre-5287-timeouts",
		"herdr workspace focus wP",
		"herdr tab create --workspace wP --cwd " + path + " --label SRE-5287 · argocd-ops#4454 --focus",
	} {
		if !e.f.called(want) {
			t.Errorf("calls = %q\nwant %q", e.f.calls, want)
		}
	}
}

func TestPRFocusesTabAlreadyOnItsCheckout(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"wP","label":"PSC"}`)
	_ = e.claims.Add("wP", "PSC", Node{"issue", "SRE-5287"})
	clone := mkRepo(t, filepath.Join(e.root, "sanity-io", "argocd-ops"))
	e.f.out["git worktree list --porcelain"] = "worktree " + clone + "\nbranch refs/heads/main\n\n" +
		"worktree /wt/ops-timeouts\nbranch refs/heads/sre-5287-timeouts\n"
	e.f.out["herdr pane list"] = `{"result":{"panes":[{"pane_id":"wP:p4","tab_id":"wP:t3","workspace_id":"wP","cwd":"/wt/ops-timeouts/charts"}]}}`

	req := Request{
		Issues: []Issue{{ID: "SRE-5287"}},
		PR:     &PR{Repo: "sanity-io/argocd-ops", Number: 4454, Branch: "sre-5287-timeouts"},
	}
	if err := e.o.Open(req); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr tab focus wP:t3") || e.f.called("herdr tab create") || e.f.called("git worktree add") {
		t.Errorf("calls = %q, want the existing tab focused", e.f.calls)
	}
}

func TestForkPRFetchesPullHead(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"w1","label":"ops#12 Add feat"}`)
	clone := mkRepo(t, filepath.Join(e.root, "sanity-io", "ops"))
	e.f.out["git worktree list --porcelain"] = "worktree " + clone + "\nbranch refs/heads/main\n"
	e.f.fail["git rev-parse --verify --quiet refs/heads/feat"] = true
	e.f.fail["git fetch --quiet origin feat"] = true

	if err := e.o.Open(Request{PR: &PR{Repo: "sanity-io/ops", Number: 12, Branch: "feat"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.wtDir, "ops", "feat")
	if !e.f.called("git fetch --quiet origin pull/12/head:feat") || !e.f.called("git worktree add "+path+" feat") {
		t.Errorf("calls = %q, want the PR head fetched into feat and checked out", e.f.calls)
	}
}

func TestStaleWorktreeRecordIsPruned(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"w1","label":"edex#6"}`)
	clone := mkRepo(t, filepath.Join(e.root, "sanity-io", "edex"))
	e.f.out["git worktree list --porcelain"] = "worktree " + clone + "\nbranch refs/heads/main\n\n" +
		"worktree /tmp/gone\nbranch refs/heads/sre-5263-x\nprunable gitdir file points to non-existent location\n"

	if err := e.o.Open(Request{PR: &PR{Repo: "sanity-io/edex", Number: 6, Branch: "sre-5263-x"}}); err != nil {
		t.Fatal(err)
	}
	prune := slices.Index(e.f.calls, "git worktree prune")
	add := slices.IndexFunc(e.f.calls, func(c string) bool { return strings.HasPrefix(c, "git worktree add") })
	if prune < 0 || add < prune {
		t.Errorf("calls = %q, want git worktree prune before the add", e.f.calls)
	}
}

func TestPRWithoutCloneExplains(t *testing.T) {
	e := newEnv(t)
	e.workspaces(`{"workspace_id":"w1","label":"missing#1"}`)
	err := e.o.Open(Request{PR: &PR{Repo: "sanity-io/missing", Number: 1, Branch: "x"}})
	if err == nil || !strings.Contains(err.Error(), "no local clone of sanity-io/missing") {
		t.Errorf("err = %v, want a missing-clone explanation", err)
	}
}

func TestSessionFocusesRunningAgent(t *testing.T) {
	e := newEnv(t)
	e.f.out["herdr agent list"] = `{"result":{"agents":[
		{"pane_id":"w1:p1","agent_session":{"kind":"id","value":"other"}},
		{"pane_id":"w4:p2","agent_session":{"kind":"id","value":"abc-123"}}]}}`

	if err := e.o.Open(Request{Session: &Session{ID: "abc-123", Cwd: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr agent focus w4:p2") || e.f.called("herdr pane run") {
		t.Errorf("calls = %q, want the running session's pane focused", e.f.calls)
	}
}

func TestSessionResumesInWorkspaceWithPaneThere(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.f.out["herdr pane list"] = `{"result":{"panes":[{"pane_id":"w3:p1","workspace_id":"w3","cwd":"` + dir + `"}]}}`
	e.f.out["herdr tab create --workspace w3 --cwd "+dir+" --label Fix CI --focus"] = `{"result":{"root_pane":{"pane_id":"w3:p7"}}}`

	s := Session{ID: "abc", Cwd: dir, Title: "Fix CI", Resume: []string{"claude", "--resume", "abc"}}
	if err := e.o.Open(Request{Session: &s}); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr workspace focus w3") || !e.f.called("herdr pane run w3:p7 claude --resume abc") {
		t.Errorf("calls = %q, want the resume run in a new tab of w3", e.f.calls)
	}
}

func TestSessionWithNoWorkspaceGetsOne(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.f.out["herdr workspace create --cwd "+dir+" --label Fix CI --focus"] = `{"result":{"workspace":{"workspace_id":"w5"},"root_pane":{"pane_id":"w5:p1"}}}`

	s := Session{ID: "abc", Cwd: dir, Title: "Fix CI", Resume: []string{"claude", "--resume", "abc"}}
	if err := e.o.Open(Request{Session: &s}); err != nil {
		t.Fatal(err)
	}
	if !e.f.called("herdr pane run w5:p1 claude --resume abc") {
		t.Errorf("calls = %q, want the resume run in the new workspace's pane", e.f.calls)
	}
}

func TestSessionMissingDir(t *testing.T) {
	e := newEnv(t)
	err := e.o.Open(Request{Session: &Session{ID: "abc", Cwd: "/nonexistent/agenda-test"}})
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v, want a missing-directory error", err)
	}
}

func TestClaimsSurviveRenameRenumberAndNewID(t *testing.T) {
	c := LoadClaims(filepath.Join(t.TempDir(), "claims.json"))
	n := Node{"project", "p1"}
	if err := c.Add("w1", "[1] PSC", n); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadClaims(c.path)
	if ws := reloaded.Owner(n, []Workspace{{ID: "w1", Label: "PSC rollout"}}); ws != "w1" {
		t.Errorf("after rename: owner = %q, want w1", ws)
	}
	if ws := reloaded.Owner(n, []Workspace{{ID: "w7", Label: "[4] PSC rollout"}}); ws != "w7" {
		t.Errorf("same label, new id: owner = %q, want w7", ws)
	}
	if ws := reloaded.Owner(n, []Workspace{{ID: "w8", Label: "Other"}}); ws != "" {
		t.Errorf("workspace gone: owner = %q, want none", ws)
	}
}

func TestClaimMovesBetweenWorkspaces(t *testing.T) {
	c := LoadClaims(filepath.Join(t.TempDir(), "claims.json"))
	n := Node{"issue", "SRE-1"}
	_ = c.Add("w1", "A", n)
	_ = c.Add("w2", "B", n)
	live := []Workspace{{ID: "w1", Label: "A"}, {ID: "w2", Label: "B"}}
	if ws := c.Owner(n, live); ws != "w2" || len(c.claims) != 1 {
		t.Errorf("owner = %q with %d claims, want w2 and w1's empty claim dropped", ws, len(c.claims))
	}
}

func TestParseWorktrees(t *testing.T) {
	got := parseWorktrees("worktree /src/ops\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /wt/feat-x\nHEAD def\nbranch refs/heads/feat/x\n\n" +
		"worktree /tmp/gone\nHEAD 123\ndetached\nprunable gitdir file points to non-existent location\n")
	want := []worktree{{"/src/ops", "main", false}, {"/wt/feat-x", "feat/x", false}, {"/tmp/gone", "", true}}
	if !slices.Equal(got, want) {
		t.Errorf("parseWorktrees = %+v", got)
	}
}

func TestMentions(t *testing.T) {
	cases := []struct {
		s, key string
		want   bool
	}{
		{"SRE-2999 Enforce", "SRE-2999", true},
		{"[7] sre-2999 enforce", "SRE-2999", true},
		{"obliadp/sre-2999-enforce", "SRE-2999", true},
		{"SRE-29990", "SRE-2999", false},
		{"XSRE-2999", "SRE-2999", false},
		{"agenda#28 Mouse", "agenda#28", true},
		{"agenda#281", "agenda#28", false},
	}
	for _, c := range cases {
		if got := mentions(c.s, c.key); got != c.want {
			t.Errorf("mentions(%q, %q) = %v, want %v", c.s, c.key, got, c.want)
		}
	}
}

func TestIssueBranchFallsBackToSlug(t *testing.T) {
	if got := issueBranch(Issue{ID: "SRE-9", Title: "Move Grafana: to v13, now please!"}); got != "sre-9-move-grafana-to-v13-now" {
		t.Errorf("issueBranch = %q", got)
	}
}

func TestShellJoinQuotes(t *testing.T) {
	got := shellJoin([]string{"claude", "--resume", "abc-123", "it's here"})
	if got != `claude --resume abc-123 'it'\''s here'` {
		t.Errorf("shellJoin = %s", got)
	}
}

func TestReposCloneIgnoresCase(t *testing.T) {
	root := t.TempDir()
	want := mkRepo(t, filepath.Join(root, "GoogleCloudPlatform", "Terraformer"))
	r := Repos{Root: root}
	if got, ok := r.Clone("googlecloudplatform/terraformer"); !ok || got != want {
		t.Errorf("Clone = %q, %v; want %q", got, ok, want)
	}
	if _, ok := r.Clone("sanity-io/nope"); ok {
		t.Error("Clone found a repo that isn't there")
	}
}
