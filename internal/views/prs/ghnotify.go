package prs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/textproto"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

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

// notifSync is the known notification state, shared by every view because
// they spend one quota. Only the first sync lists everything; later ones ask
// for what changed since and merge by thread id.
var notifSync struct {
	mu      sync.Mutex
	threads map[string]ghThread
	lastMod string    // Last-Modified of the newest first page
	since   time.Time // start of the last successful sync
	fullAt  time.Time
}

// fullResync bounds how long a thread read elsewhere can go unnoticed: a
// read may not bump the thread's updated time, so since= alone can miss it.
const fullResync = time.Hour

// Package variable so tests stand in for gh.
var ghRun = func(args ...string) ([]byte, error) {
	return exec.Command("gh", args...).Output()
}

// getNotifPage fetches one page with response headers. On a 304 gh exits
// non-zero but the headers are on stdout, so the status is read first.
func getNotifPage(path, ifModified string) (header http.Header, body []byte, notModified bool, err error) {
	args := []string{"api", "-i", path}
	if ifModified != "" {
		args = append(args, "-H", "If-Modified-Since: "+ifModified)
	}
	out, err := ghRun(args...)
	head, body, _ := bytes.Cut(out, []byte("\r\n\r\n"))
	status, rest, _ := bytes.Cut(head, []byte("\n"))
	if bytes.Contains(status, []byte(" 304")) {
		return nil, nil, true, nil
	}
	if err != nil {
		return nil, nil, false, cmdErrOut(err, out)
	}
	h, perr := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(rest, "\r\n\r\n"...)))).ReadMIMEHeader()
	if perr != nil {
		return nil, nil, false, perr
	}
	return http.Header(h), body, false, nil
}

// syncThreads returns the PR keys whose notification is read, calling the
// API as little as possible. ok is false when the sync could not run.
func syncThreads() (read map[string]bool, ok bool) {
	notifSync.mu.Lock()
	defer notifSync.mu.Unlock()
	st := &notifSync
	started := ghNow()
	full := st.threads == nil || started.Sub(st.fullAt) >= fullResync
	// all=true is the point: the default list omits read threads, which
	// are precisely the ones that clear a local mark.
	query := "/notifications?all=true&per_page=100"
	ifMod := st.lastMod
	if !full {
		query += "&since=" + st.since.Add(-time.Minute).UTC().Format(time.RFC3339)
	} else {
		ifMod = ""
	}
	fresh := map[string]ghThread{}
	lastMod := ""
	for page := 1; ; page++ {
		path := query
		if page > 1 {
			path += "&page=" + strconv.Itoa(page)
		}
		h, body, notModified, err := getNotifPage(path, ifMod)
		if err != nil {
			return nil, false
		}
		if notModified {
			return readKeys(st.threads), true
		}
		ifMod = ""
		if page == 1 {
			lastMod = h.Get("Last-Modified")
		}
		var threads []ghThread
		if json.Unmarshal(body, &threads) != nil {
			return nil, false
		}
		for _, t := range threads {
			fresh[t.ID] = t
		}
		if !strings.Contains(h.Get("Link"), `rel="next"`) {
			break
		}
	}
	if full {
		st.threads, st.fullAt = fresh, started
	} else {
		for id, t := range fresh {
			st.threads[id] = t
		}
	}
	st.lastMod, st.since = lastMod, started
	return readKeys(st.threads), true
}

func readKeys(threads map[string]ghThread) map[string]bool {
	read := map[string]bool{}
	for _, t := range threads {
		if k := t.key(); k != "" && !t.Unread {
			read[k] = true
		}
	}
	return read
}

// fetchReadThreads reports which PR notifications are read. Silent on
// failure: a sync that cannot run leaves the local marks exactly as they were.
func fetchReadThreads() tea.Cmd {
	if _, paused := rateLimitedUntil(); paused {
		return nil
	}
	return func() tea.Msg {
		read, ok := syncThreads()
		if !ok {
			return nil
		}
		return threadsReadMsg{read: read}
	}
}

// markThreadRead marks the GitHub notification for one PR read, so agenda
// and github.com/notifications agree. Best-effort and silent: this is a
// courtesy sync, and failing it must never interrupt reading a PR.
func markThreadRead(repo string, number int) tea.Cmd {
	if _, paused := rateLimitedUntil(); paused {
		return nil
	}
	return func() tea.Msg {
		out, err := exec.Command("gh", "api", "/notifications", "--paginate").Output()
		if err != nil {
			_ = cmdErr(err)
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
