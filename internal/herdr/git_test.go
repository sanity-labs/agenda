package herdr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepos makes a bare "origin" with main and a pushed feature branch, and a
// clone of it whose local copy of the feature branch is deleted, so the
// branch exists only on origin, the way a PR head does.
func gitRepos(t *testing.T) (clone, origin string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	origin, clone = filepath.Join(dir, "origin.git"), filepath.Join(dir, "ops")
	g := func(in string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = in
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g(dir, "init", "-q", "--bare", "-b", "main", origin)
	g(dir, "clone", "-q", origin, clone)
	g(clone, "commit", "-q", "--allow-empty", "-m", "init")
	g(clone, "push", "-q", "origin", "main")
	g(clone, "remote", "set-head", "origin", "main")
	g(clone, "switch", "-q", "-c", "feat")
	g(clone, "commit", "-q", "--allow-empty", "-m", "feature work")
	g(clone, "push", "-q", "origin", "feat")
	g(clone, "switch", "-q", "main")
	g(clone, "branch", "-q", "-D", "feat")
	return clone, origin
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestCheckoutWithRealGit(t *testing.T) {
	clone, _ := gitRepos(t)
	o := &Opener{run: ExecRunner, worktreesDir: filepath.Join(t.TempDir(), "wt")}

	// A PR head that exists only on origin: fetched, checked out at its
	// tip, tracking origin. Paths come back with symlinks resolved (on
	// macOS the temp dir is under /var, a link to /private/var), the form
	// git and Herdr report them in.
	path, err := o.checkout(clone, "feat", &PR{Repo: "x/ops", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := realPath(filepath.Join(o.worktreesDir, "ops", "feat")); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if msg := gitOut(t, path, "log", "-1", "--format=%s"); msg != "feature work" {
		t.Errorf("checked out %q, want the PR's commit", msg)
	}
	if up := gitOut(t, path, "rev-parse", "--abbrev-ref", "feat@{upstream}"); up != "origin/feat" {
		t.Errorf("upstream = %q, want origin/feat", up)
	}

	// Asking again reuses that worktree rather than failing on a second add.
	if again, err := o.checkout(clone, "feat", &PR{Repo: "x/ops", Number: 1}); err != nil || again != path {
		t.Errorf("second checkout = %q, %v; want the same worktree", again, err)
	}

	// A new issue branch, slash flattened in the path: off origin/main, and
	// tracking nothing.
	path, err = o.checkout(clone, "obliadp/sre-1-fix", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := realPath(filepath.Join(o.worktreesDir, "ops", "obliadp-sre-1-fix")); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if msg := gitOut(t, path, "log", "-1", "--format=%s"); msg != "init" {
		t.Errorf("issue branch starts at %q, want main's tip", msg)
	}
	if up := gitOut(t, path, "rev-parse", "--abbrev-ref", "obliadp/sre-1-fix@{upstream}"); up != "" {
		t.Errorf("issue branch tracks %q, want nothing", up)
	}

	// The worktree's directory vanishes (a cleaned-up scratch dir): the
	// stale record is pruned and the branch checked out afresh.
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if _, err := o.checkout(clone, "obliadp/sre-1-fix", nil); err != nil {
		t.Errorf("checkout after the worktree's directory vanished: %v", err)
	}
}
