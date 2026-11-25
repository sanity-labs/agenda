package herdr

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// worktree is one checkout of a repository, as git lists it.
type worktree struct {
	path     string
	branch   string // "" when detached
	prunable bool   // registered, but its directory is gone
}

// worktreesOf lists repo's checkouts, the main checkout included.
func (o *Opener) worktreesOf(repo string) ([]worktree, error) {
	out, err := o.run(repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, commandError("git worktree list", err)
	}
	return parseWorktrees(string(out)), nil
}

// parseWorktrees reads `git worktree list --porcelain`: a "worktree <path>"
// line opens each record, followed by attribute lines.
func parseWorktrees(s string) []worktree {
	var out []worktree
	for _, line := range strings.Split(s, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			out = append(out, worktree{path: p})
			continue
		}
		if len(out) == 0 {
			continue
		}
		cur := &out[len(out)-1]
		switch {
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.prunable = true
		}
	}
	return out
}

// checkout returns a worktree of clone on branch, adding one when there is
// none. New worktrees go under the worktrees directory as <repo>/<branch>,
// slashes flattened, the way Herdr lays out its own.
func (o *Opener) checkout(clone, branch string, pr *PR) (string, error) {
	wts, err := o.worktreesOf(clone)
	if err != nil {
		return "", err
	}
	stale := false
	for _, wt := range wts {
		if wt.branch != branch {
			continue
		}
		if wt.prunable {
			stale = true
			continue
		}
		return realPath(wt.path), nil // git won't check a branch out twice: use it where it is
	}
	if stale {
		// git still counts the branch as checked out in a worktree whose
		// directory is gone (a cleaned-up scratch checkout) and would refuse
		// it; prune drops only such records.
		if err := o.git(clone, "worktree", "prune"); err != nil {
			return "", commandError("git worktree prune", err)
		}
	}

	path := filepath.Join(o.worktreesDir, filepath.Base(clone), strings.ReplaceAll(branch, "/", "-"))
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s exists but is not a worktree of %s", path, clone)
	}
	args, err := o.addArgs(clone, branch, path, pr)
	if err != nil {
		return "", err
	}
	if _, err := o.run(clone, "git", args...); err != nil {
		return "", commandError("git worktree add", err)
	}
	return realPath(path), nil
}

// realPath resolves symlinks in p, the form git and Herdr report directories
// in (/private/tmp rather than /tmp on macOS), so paths from all three
// compare equal. A path that can't be resolved is returned as is.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// addArgs readies branch and returns the `git worktree add` that checks it
// out at path: a local branch as it is; a PR's head fetched from origin and
// tracking it (or, for a fork, GitHub's pull/N/head); otherwise a new branch
// off origin's freshly fetched default branch, untracked so a pull doesn't
// merge main into it.
func (o *Opener) addArgs(clone, branch, path string, pr *PR) ([]string, error) {
	if o.git(clone, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil {
		return []string{"worktree", "add", path, branch}, nil
	}
	if pr != nil {
		if o.git(clone, "fetch", "--quiet", "origin", branch) == nil {
			return []string{"worktree", "add", "--track", "-b", branch, path, "origin/" + branch}, nil
		}
		ref := "pull/" + strconv.Itoa(pr.Number) + "/head:" + branch
		if err := o.git(clone, "fetch", "--quiet", "origin", ref); err != nil {
			return nil, commandError("git fetch "+ref, err)
		}
		return []string{"worktree", "add", path, branch}, nil
	}
	base := o.defaultBranch(clone)
	if base == "" {
		return []string{"worktree", "add", "-b", branch, path}, nil // no origin default: from HEAD
	}
	_ = o.git(clone, "fetch", "--quiet", "origin", strings.TrimPrefix(base, "origin/"))
	return []string{"worktree", "add", "--no-track", "-b", branch, path, base}, nil
}

// defaultBranch is origin's default branch ("origin/main"), or "".
func (o *Opener) defaultBranch(clone string) string {
	out, err := o.run(clone, "git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	for _, b := range []string{"origin/main", "origin/master"} {
		if o.git(clone, "rev-parse", "--verify", "--quiet", b) == nil {
			return b
		}
	}
	return ""
}

// headBranch asks GitHub for a PR's head branch.
func (o *Opener) headBranch(pr PR) (string, error) {
	out, err := o.run("", "gh", "pr", "view", strconv.Itoa(pr.Number), "-R", pr.Repo,
		"--json", "headRefName", "-q", ".headRefName")
	if err != nil {
		return "", commandError("gh pr view", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (o *Opener) git(dir string, args ...string) error {
	_, err := o.run(dir, "git", args...)
	return err
}

// findWorktreeDir looks under the worktrees directory for a checkout whose
// path mentions the issue, laid out as <repo>/<branch>.
func (o *Opener) findWorktreeDir(id string) string {
	if o.worktreesDir == "" {
		return ""
	}
	var found string
	_ = filepath.WalkDir(o.worktreesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == o.worktreesDir || !isRepo(path) {
			return nil // unreadable, a file, or a directory above the checkouts
		}
		if rel, _ := filepath.Rel(o.worktreesDir, path); mentions(rel, id) {
			found = path
			return filepath.SkipAll
		}
		return filepath.SkipDir // never descend into a checkout
	})
	return found
}
