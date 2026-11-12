package prs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeGit stands in for execIn: it records each call as "name args" and
// fails the first one starting with a prefix in fail.
type fakeGit struct {
	calls []string
	wt    string
	fail  map[string]string
}

func stubGit(t *testing.T, f *fakeGit) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	old := execIn
	t.Cleanup(func() { execIn = old })
	execIn = func(dir, name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		if len(args) > 3 && args[0] == "worktree" && args[1] == "add" {
			f.wt = args[3]
		}
		if f.wt != "" {
			call = strings.ReplaceAll(call, f.wt, "<wt>")
		}
		f.calls = append(f.calls, call)
		for prefix, out := range f.fail {
			if strings.HasPrefix(call, prefix) {
				return out, errors.New("exit status 1")
			}
		}
		switch {
		case name == "ghq":
			return "/src/github.com/o/r", nil
		case strings.HasPrefix(call, "git config --get remote.origin.url"):
			return "git@github.com:o/r.git", nil
		case strings.HasPrefix(call, "git rev-parse"):
			return "abc123", nil
		case strings.HasPrefix(call, "git diff --name-only"):
			return "a.go\nb.go", nil
		}
		return "", nil
	}
}

func (f *fakeGit) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func behind(num int) pr {
	p := mergeable(num)
	p.BaseRefName, p.HeadRefName, p.ViewerCanUpdateBranch = "main", "feat", true
	return p
}

func TestRebaseCommandOnlyWhenBehindOrConflicting(t *testing.T) {
	if got := titles(mergeView(t, mergeable(7))); strings.Contains(got, "Rebase") {
		t.Errorf("an up-to-date PR offers no rebase: %s", got)
	}
	if got := titles(mergeView(t, behind(7))); !strings.Contains(got, "Rebase onto main") {
		t.Errorf("a PR behind its base offers a rebase: %s", got)
	}
	c := mergeable(7)
	c.Mergeable, c.BaseRefName = "CONFLICTING", "main"
	if got := titles(mergeView(t, c)); !strings.Contains(got, "Rebase onto main") {
		t.Errorf("a conflicting PR offers a rebase: %s", got)
	}
}

func TestRebaseAsksFirstAndNamesTheAuthor(t *testing.T) {
	f := &fakeGit{}
	stubGit(t, f)
	p := behind(7)
	p.Author.Login = "someone"
	v := mergeView(t, p)
	command(t, v, "rebase")()
	if v.review == nil || v.review.confirm != "rebase" || v.review.submitting {
		t.Fatalf("rebase should stage a confirmation, got %+v", v.review)
	}
	if out := v.Overlay(); !strings.Contains(out, "feat onto main") || !strings.Contains(out, "@someone") {
		t.Errorf("confirmation should name the branches and the author:\n%s", out)
	}
	v.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if v.review != nil || len(f.calls) > 0 {
		t.Fatalf("any other key cancels and runs nothing: %+v %v", v.review, f.calls)
	}
	command(t, v, "rebase")()
	if cmd := v.Update(tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd == nil || !v.review.submitting {
		t.Error("y should start the rebase")
	}
}

func TestRebaseRefusesAFork(t *testing.T) {
	f := &fakeGit{}
	stubGit(t, f)
	p := behind(7)
	p.IsCrossRepository = true
	v := mergeView(t, p)
	command(t, v, "rebase")()
	if v.review != nil || !strings.Contains(v.flash, "fork") || len(f.calls) > 0 {
		t.Errorf("a fork's branch is refused before anything runs: %+v %q %v", v.review, v.flash, f.calls)
	}
}

func TestRebaseCommandSequence(t *testing.T) {
	f := &fakeGit{}
	stubGit(t, f)
	if err := rebaseLocally("o/r", "main", "feat"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ghq list --exact --full-path github.com/o/r",
		"git config --get remote.origin.url",
		"git fetch origin +refs/heads/main:refs/remotes/origin/main +refs/heads/feat:refs/remotes/origin/feat",
		"git rev-parse refs/remotes/origin/feat",
		"git worktree add --detach <wt> abc123",
		"git rebase --no-update-refs refs/remotes/origin/main",
		"git push origin HEAD:refs/heads/feat --force-with-lease=refs/heads/feat:abc123",
		"git worktree remove --force <wt>",
		"git worktree prune",
	}
	if got := strings.Join(f.calls, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestRebaseConflictAbortsAndPushesNothing(t *testing.T) {
	f := &fakeGit{fail: map[string]string{"git rebase --no-update-refs": "CONFLICT (content)"}}
	stubGit(t, f)
	err := rebaseLocally("o/r", "main", "feat")
	if err == nil || !strings.Contains(err.Error(), "a.go, b.go") {
		t.Errorf("error should list the conflicting files: %v", err)
	}
	if !f.ran("git rebase --abort") || f.ran("git push") {
		t.Errorf("a conflict aborts and pushes nothing: %v", f.calls)
	}
}

func TestRebaseRemovesTheWorktreeOnEveryPath(t *testing.T) {
	for _, step := range []string{"", "git worktree add", "git rebase --no-update-refs", "git push"} {
		f := &fakeGit{fail: map[string]string{}}
		if step != "" {
			f.fail[step] = "error: nope"
		}
		stubGit(t, f)
		err := rebaseLocally("o/r", "main", "feat")
		if (err != nil) != (step != "") {
			t.Errorf("%q: err = %v", step, err)
		}
		if !f.ran("git worktree remove --force <wt>") {
			t.Errorf("%q: worktree not removed: %v", step, f.calls)
		}
		if left, _ := os.ReadDir(os.TempDir()); len(left) > 0 {
			t.Errorf("%q: temp dir left behind: %v", step, left)
		}
	}
}

// ghq exits 0 with no output for a repo it has no clone of.
func TestRebaseWithoutALocalClone(t *testing.T) {
	for _, ghqErr := range []error{nil, errors.New("ghq: not found")} {
		f := &fakeGit{}
		stubGit(t, f)
		old := execIn
		execIn = func(dir, name string, args ...string) (string, error) {
			if name == "ghq" {
				f.calls = append(f.calls, name)
				return "", ghqErr
			}
			return old(dir, name, args...)
		}
		err := rebaseLocally("o/r", "main", "feat")
		if err == nil || !strings.Contains(err.Error(), "no local clone of o/r") || len(f.calls) != 1 {
			t.Errorf("ghq err %v: %v after %v", ghqErr, err, f.calls)
		}
	}
}

func TestRebaseRefusesAMismatchedOrigin(t *testing.T) {
	f := &fakeGit{}
	stubGit(t, f)
	old := execIn
	execIn = func(dir, name string, args ...string) (string, error) {
		if strings.Join(args, " ") == "config --get remote.origin.url" {
			return "git@github.com:me/r-fork.git", nil
		}
		return old(dir, name, args...)
	}
	if err := rebaseLocally("o/r", "main", "feat"); err == nil || f.ran("git fetch") {
		t.Errorf("a clone whose origin is another repo is refused: %v %v", err, f.calls)
	}
}

// TestRebaseRealGit runs the real thing against throwaway local repos: the
// branch lands on the new base, the user's checkout is untouched, and the
// signing config git reads is the user's own.
func TestRebaseRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	signer := filepath.Join(dir, "fake-gpg")
	// Enough of gpg's protocol for git to accept a signature.
	script := "#!/bin/sh\ncat >/dev/null\necho '[GNUPG:] SIG_CREATED ' >&2\n" +
		"printf -- '-----BEGIN PGP SIGNATURE-----\\n\\nfake\\n-----END PGP SIGNATURE-----\\n'\n"
	if err := os.WriteFile(signer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(dir, "gitconfig")
	cfg := "[user]\n\tname = t\n\temail = t@t\n\tsigningkey = x\n[init]\n\tdefaultBranch = main\n" +
		"[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = " + signer + "\n"
	if err := os.WriteFile(global, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	git := func(in string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = in
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(in, file string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(in, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		git(in, "add", file)
		git(in, "commit", "-q", "--no-gpg-sign", "-m", file)
	}
	origin := filepath.Join(dir, "origin.git")
	clone := filepath.Join(dir, "src", "github.com", "o", "r")
	git(dir, "init", "-q", "--bare", origin)
	git(dir, "clone", "-q", origin, clone)
	commit(clone, "base1")
	git(clone, "push", "-q", "origin", "main")
	git(clone, "checkout", "-q", "-b", "feat")
	commit(clone, "feat1")
	git(clone, "push", "-q", "origin", "feat")
	git(clone, "checkout", "-q", "main")
	commit(clone, "base2")
	git(clone, "push", "-q", "origin", "main")
	git(clone, "checkout", "-q", "feat")
	git(clone, "remote", "set-url", "origin", "git@github.com:o/r.git")
	git(clone, "config", "url."+origin+".insteadOf", "git@github.com:o/r.git")

	old := execIn
	t.Cleanup(func() { execIn = old })
	execIn = func(in, name string, args ...string) (string, error) {
		if name == "ghq" {
			return clone, nil
		}
		return old(in, name, args...)
	}
	before := git(clone, "rev-parse", "HEAD")
	if err := rebaseLocally("o/r", "main", "feat"); err != nil {
		t.Fatal(err)
	}
	if got, want := git(origin, "rev-parse", "feat~1"), git(origin, "rev-parse", "main"); got != want {
		t.Errorf("feat~1 = %s, want main %s", got, want)
	}
	if !strings.Contains(git(origin, "cat-file", "commit", "feat"), "gpgsig") {
		t.Error("the rebased commit was not signed with the user's config")
	}
	if git(clone, "rev-parse", "HEAD") != before || git(clone, "status", "--porcelain") != "" {
		t.Error("the user's checkout moved")
	}
	if wts := git(clone, "worktree", "list"); strings.Count(wts, "\n") != 0 {
		t.Errorf("temp worktree left registered:\n%s", wts)
	}
}
