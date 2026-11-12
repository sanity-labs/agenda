package prs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// execIn runs a command in dir and returns its trimmed combined output.
// Tests replace it so nothing touches a real repository.
var execIn = func(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// askRebase stages the local rebase for a yes: it force-pushes a branch
// other people may have checked out.
func (v *View) askRebase(p pr) tea.Cmd {
	if p.IsCrossRepository {
		v.flash = ui.Red.Render("cannot rebase " + p.repo() + "#" + fmt.Sprint(p.Number) + ": its branch lives in a fork")
		return nil
	}
	v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number, verdict: "rebase", confirm: "rebase"}
	if !p.ViewerDidAuthor && p.Author.Login != "" {
		v.review.warn = "this is @" + p.Author.Login + "'s branch"
	}
	return nil
}

func (v *View) submitRebase() tea.Cmd {
	r := v.review
	p, ok := v.prByURL(r.url)
	if !ok {
		r.fail = "this PR is no longer loaded; refresh and try again"
		return nil
	}
	r.submitting, r.action = true, "rebase"
	what := fmt.Sprintf("rebased %s#%d onto %s", p.repo(), p.Number, p.BaseRefName)
	return func() tea.Msg {
		err := rebaseLocally(p.repo(), p.BaseRefName, p.HeadRefName)
		if err != nil {
			what = "rebase failed"
		}
		return reviewDoneMsg{from: v, what: what, url: p.URL, err: err, toast: true}
	}
}

// rebaseLocally works in a throwaway worktree so no checkout of the user's
// moves; --no-update-refs keeps rebase.updateRefs off their local branches.
func rebaseLocally(repo, base, head string) error {
	out, err := execIn("", "ghq", "list", "--exact", "--full-path", "github.com/"+repo)
	clone, _, _ := strings.Cut(out, "\n")
	if err != nil || clone == "" {
		return fmt.Errorf("no local clone of %s found with ghq", repo)
	}
	git := func(dir string, args ...string) (string, error) {
		out, err := execIn(dir, "git", args...)
		if err != nil {
			return out, fmt.Errorf("git %s: %s", args[0], gitMsg(out, err))
		}
		return out, nil
	}
	url, err := git(clone, "config", "--get", "remote.origin.url")
	if err != nil {
		return err
	}
	if u := strings.ToLower(strings.TrimSuffix(url, ".git")); !strings.HasSuffix(u, "/"+strings.ToLower(repo)) && !strings.HasSuffix(u, ":"+strings.ToLower(repo)) {
		return fmt.Errorf("origin of %s is %s, not %s", clone, url, repo)
	}
	if _, err := git(clone, "fetch", "origin",
		"+refs/heads/"+base+":refs/remotes/origin/"+base,
		"+refs/heads/"+head+":refs/remotes/origin/"+head); err != nil {
		return err
	}
	lease, err := git(clone, "rev-parse", "refs/remotes/origin/"+head)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "agenda-rebase-")
	if err != nil {
		return err
	}
	wt := filepath.Join(tmp, "wt")
	defer func() {
		_, _ = execIn(clone, "git", "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(tmp)
		_, _ = execIn(clone, "git", "worktree", "prune")
	}()
	if _, err := git(clone, "worktree", "add", "--detach", wt, lease); err != nil {
		return err
	}
	if out, rerr := execIn(wt, "git", "rebase", "--no-update-refs", "refs/remotes/origin/"+base); rerr != nil {
		files, _ := execIn(wt, "git", "diff", "--name-only", "--diff-filter=U")
		_, _ = execIn(wt, "git", "rebase", "--abort")
		if files == "" {
			return fmt.Errorf("git rebase: %s; nothing pushed", gitMsg(out, rerr))
		}
		return fmt.Errorf("conflicts in %s; nothing pushed", strings.Join(strings.Fields(files), ", "))
	}
	_, err = git(wt, "push", "origin", "HEAD:refs/heads/"+head, "--force-with-lease=refs/heads/"+head+":"+lease)
	return err
}

// gitMsg is the line of git's output that says what went wrong.
func gitMsg(out string, err error) string {
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") || strings.Contains(l, "[rejected]") {
			return strings.TrimSpace(l)
		}
	}
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}
