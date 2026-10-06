package herdr

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Repos finds local clones of GitHub repositories: an explicit override, else
// <Root>/<owner>/<repo>.
type Repos struct {
	Root      string            // e.g. ~/git (already expanded)
	Overrides map[string]string // "owner/repo" -> clone path (already expanded)
}

// Clone returns the local clone of the GitHub repository slug ("owner/repo").
// GitHub names are case-insensitive, so a directory that differs only in case
// matches too.
func (r Repos) Clone(slug string) (string, bool) {
	for s, p := range r.Overrides {
		if strings.EqualFold(s, slug) && isRepo(p) {
			return p, true
		}
	}
	owner, name, ok := strings.Cut(slug, "/")
	if !ok || r.Root == "" {
		return "", false
	}
	// Resolve through the listings rather than joining the slug: on a
	// case-insensitive filesystem the join would "exist" but carry the
	// slug's case instead of the directory's.
	ownerDir := matchDir(r.Root, owner)
	if ownerDir == "" {
		return "", false
	}
	if p := matchDir(ownerDir, name); p != "" && isRepo(p) {
		return p, true
	}
	return "", false
}

// Candidate is a local clone offered in the repository picker.
type Candidate struct {
	Slug   string // "owner/repo" as laid out under Root, or an override's key
	Path   string
	Recent time.Time // last git activity (fetch, checkout, commit)
}

// List returns every clone under Root and in Overrides, most recently used
// first.
func (r Repos) List() []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(slug, path string) {
		if seen[path] || !isRepo(path) {
			return
		}
		seen[path] = true
		out = append(out, Candidate{Slug: slug, Path: path, Recent: lastActivity(path)})
	}
	for s, p := range r.Overrides {
		add(s, p)
	}
	if r.Root != "" {
		owners, _ := os.ReadDir(r.Root)
		for _, o := range owners {
			if !o.IsDir() || strings.HasPrefix(o.Name(), ".") {
				continue
			}
			repos, _ := os.ReadDir(filepath.Join(r.Root, o.Name()))
			for _, e := range repos {
				if e.IsDir() {
					add(o.Name()+"/"+e.Name(), filepath.Join(r.Root, o.Name(), e.Name()))
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Recent.After(out[j].Recent) })
	return out
}

// Containing returns the candidate whose clone contains path, or -1.
func Containing(cands []Candidate, path string) int {
	best, bestLen := -1, 0
	for i, c := range cands {
		if within(path, c.Path) && len(c.Path) > bestLen {
			best, bestLen = i, len(c.Path)
		}
	}
	return best
}

// within reports whether path is dir or lies inside it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// isRepo reports whether dir is a git checkout (a .git directory, or the
// .git file of a linked worktree).
func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// matchDir finds the directory in parent named name: an exact match, else
// one that differs only in case.
func matchDir(parent, name string) string {
	entries, _ := os.ReadDir(parent)
	folded := ""
	for _, e := range entries {
		switch {
		case !e.IsDir():
		case e.Name() == name:
			return filepath.Join(parent, name)
		case folded == "" && strings.EqualFold(e.Name(), name):
			folded = filepath.Join(parent, e.Name())
		}
	}
	return folded
}

// lastActivity approximates when a clone was last used: the newest of the
// files git rewrites on fetch (FETCH_HEAD), checkout (HEAD) and staging
// (index).
func lastActivity(repo string) time.Time {
	var t time.Time
	for _, f := range []string{"FETCH_HEAD", "HEAD", "index"} {
		if fi, err := os.Stat(filepath.Join(repo, ".git", f)); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
