package prs

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ghThread is one GitHub notification thread, reduced to what is needed to
// match it against a PR row.
type ghThread struct {
	ID      string `json:"id"`
	Unread  bool   `json:"unread"`
	Subject struct {
		// URL is the API form, /repos/OWNER/REPO/pulls/N, not the HTML URL
		// the rows carry, so matching goes through repo and number.
		URL string `json:"url"`
	} `json:"subject"`
}

// key is "OWNER/REPO#N", or "" when the subject is not a pull request.
func (t ghThread) key() string {
	// Anchor on /repos/ rather than counting from the end: a shorter or
	// unexpected path would otherwise yield a key that matches the wrong PR.
	_, rest, ok := strings.Cut(t.Subject.URL, "/repos/")
	if !ok {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) != 4 {
		return ""
	}
	owner, repo, kind, num := parts[0], parts[1], parts[2], parts[3]
	if kind != "pulls" && kind != "issues" {
		return ""
	}
	if owner == "" || repo == "" || num == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s#%s", owner, repo, num)
}

// threadsReadMsg carries the set of PR keys ("OWNER/REPO#N") whose GitHub
// notification is already read, so marks cleared elsewhere clear here too.
type threadsReadMsg struct{ read map[string]bool }

// fetchReadThreads lists notifications including the read ones and reports
// which are read. Silent on failure: a sync that cannot run leaves the
// local marks exactly as they were.
func fetchReadThreads() tea.Cmd {
	return func() tea.Msg {
		// all=true is the point: the default list omits read threads, which
		// are precisely the ones that clear a local mark.
		out, err := exec.Command("gh", "api", "/notifications?all=true", "--paginate").Output()
		if err != nil {
			return nil
		}
		var threads []ghThread
		if json.Unmarshal(out, &threads) != nil {
			return nil
		}
		read := map[string]bool{}
		for _, t := range threads {
			if k := t.key(); k != "" && !t.Unread {
				read[k] = true
			}
		}
		return threadsReadMsg{read: read}
	}
}

// markThreadRead marks the GitHub notification for one PR read, so agenda
// and github.com/notifications agree. Best-effort and silent: this is a
// courtesy sync, and failing it must never interrupt reading a PR.
func markThreadRead(repo string, number int) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("gh", "api", "/notifications", "--paginate").Output()
		if err != nil {
			return nil
		}
		var threads []ghThread
		if json.Unmarshal(out, &threads) != nil {
			return nil
		}
		want := fmt.Sprintf("%s#%d", repo, number)
		for _, t := range threads {
			if t.key() == want {
				_ = exec.Command("gh", "api", "-X", "PATCH", "/notifications/threads/"+t.ID).Run()
				return nil
			}
		}
		return nil
	}
}
