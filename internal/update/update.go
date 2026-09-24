// Package update checks whether a newer agenda release exists. It only ever
// reports: replacing the binary is left to whatever installed it (go install,
// Homebrew, a tarball), which owns that file and its bookkeeping.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const latestURL = "https://api.github.com/repos/sanity-labs/agenda/releases/latest"

// Release is the newer release found by Check.
type Release struct {
	Version string // normalized, no leading "v"
	URL     string
}

// Result reports the outcome of a check. Available is false when the running
// build is current, unreleased (devel), or the check failed.
type Result struct {
	Available bool
	Current   string
	Latest    Release
	Err       error
}

// HowToUpdate is the command that upgrades this binary, inferred from where
// it lives. Package managers own their files, so agenda never writes to them.
func HowToUpdate() string {
	exe, err := os.Executable()
	if err != nil {
		return "reinstall agenda from https://github.com/sanity-labs/agenda/releases"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	switch {
	case strings.Contains(exe, "/Cellar/"), strings.Contains(exe, "/homebrew/"):
		return "brew upgrade agenda"
	case strings.Contains(exe, "/pkg/mod/"), strings.Contains(exe, "/go/bin/"),
		strings.Contains(exe, "/.asdf/"), strings.Contains(exe, "/mise/"):
		return "go install github.com/sanity-labs/agenda@latest"
	default:
		return "download the latest release from https://github.com/sanity-labs/agenda/releases"
	}
}

// Check asks GitHub for the latest release and compares it to current.
// A current version that isn't a release (devel builds) never reports an
// update: there is nothing meaningful to compare against.
func Check(ctx context.Context, current string) Result {
	res := Result{Current: current}
	cur, ok := parse(current)
	if !ok {
		return res
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		res.Err = err
		return res
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		res.Err = err
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		res.Err = fmt.Errorf("github: %s", resp.Status)
		return res
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		res.Err = err
		return res
	}
	if body.Draft {
		return res
	}
	latest, ok := parse(body.TagName)
	if !ok {
		return res
	}
	res.Latest = Release{Version: strings.TrimPrefix(body.TagName, "v"), URL: body.HTMLURL}
	res.Available = newer(latest, cur)
	return res
}

// semver is a parsed major.minor.patch; pre-release and build metadata are
// dropped, so a pre-release never counts as newer than its own release.
type semver struct{ major, minor, patch int }

func parse(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		out[i] = n
	}
	return semver{out[0], out[1], out[2]}, true
}

func newer(a, b semver) bool {
	switch {
	case a.major != b.major:
		return a.major > b.major
	case a.minor != b.minor:
		return a.minor > b.minor
	default:
		return a.patch > b.patch
	}
}

// Timeout bounds a background check so a hung network never stalls a caller.
const Timeout = 5 * time.Second
