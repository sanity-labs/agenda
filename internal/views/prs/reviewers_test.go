package prs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// fakeGH puts a gh on PATH that answers the candidate queries and logs every
// other call; with teamsFail, the teams endpoint errors like a 403.
func fakeGH(t *testing.T, teamsFail bool) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	teams := `echo '{"slug":"sre","name":"SRE"}'`
	if teamsFail {
		teams = `echo "HTTP 403" >&2; exit 1`
	}
	script := `#!/bin/sh
case "$*" in
*assignableUsers*) echo '{"data":{"repository":{"assignableUsers":{"pageInfo":{"hasNextPage":false},"nodes":[{"login":"zed","name":"Zed"},{"login":"ana","name":"Ana Silva"},{"login":"bob","name":"Alice Brown"},{"login":"alice","name":null},{"login":"me","name":"The Author"}]}}}}' ;;
*suggestedReviewers*) echo '{"data":{"repository":{"pullRequest":{"suggestedReviewers":[{"reviewer":{"login":"zed"}}]}}}}' ;;
*/teams*) ` + teams + ` ;;
*) echo "$@" >> ` + log + ` ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// load runs the fetch the palette starts on open and applies its messages.
func load(t *testing.T, v *View) {
	t.Helper()
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		if b, ok := c().(tea.BatchMsg); ok {
			for _, c := range b {
				run(c)
			}
		}
	}
	run(v.PrepareCommands())
}

func reviewerView(t *testing.T) (*View, ui.Menu) {
	p := mergeable(7)
	p.Author.Login = "me"
	v := mergeView(t, p)
	load(t, v)
	var m *ui.Menu
	for _, c := range v.Commands() {
		if c.ID == "add-reviewer" {
			m = c.Next()
		}
	}
	return v, *m
}

func rowTitles(cs []ui.Command) string {
	var s []string
	for _, c := range cs {
		s = append(s, c.Title)
	}
	return strings.Join(s, "|")
}

func TestReviewerMenuLeadsWithSuggestionsAndSkipsTheAuthor(t *testing.T) {
	fakeGH(t, false)
	_, m := reviewerView(t)
	got := rowTitles(m.Match(""))
	if !strings.HasPrefix(got, "zed · Zed (suggested)|alice|ana · Ana Silva|bob") || !strings.HasSuffix(got, "o/sre · SRE") {
		t.Errorf("empty query rows = %s", got)
	}
	if strings.Contains(got, "me ·") {
		t.Errorf("the author is offered: %s", got)
	}
}

func TestReviewerMenuRanksLoginOverName(t *testing.T) {
	fakeGH(t, false)
	_, m := reviewerView(t)
	if got := rowTitles(m.Match("ali")); got != "alice|bob · Alice Brown" {
		t.Errorf("ali = %s", got)
	}
	if got := rowTitles(m.Match("silva ana")); got != "ana · Ana Silva" {
		t.Errorf("name words in any order = %s", got)
	}
	if got := rowTitles(m.Match("sre")); !strings.Contains(got, "o/sre · SRE") {
		t.Errorf("team slug = %s", got)
	}
	if got := rowTitles(m.Match("nobody")); got != "" {
		t.Errorf("nobody = %s", got)
	}
	if fb := m.Fallback("@carol"); len(fb) != 1 || fb[0].ID != "reviewer:carol" {
		t.Errorf("fallback = %+v", fb)
	}
}

func TestReviewerTeamsFailureDegradesToUsers(t *testing.T) {
	fakeGH(t, true)
	v, m := reviewerView(t)
	if got := rowTitles(m.Match("")); !strings.Contains(got, "ana") || strings.Contains(got, "SRE") {
		t.Errorf("rows = %s", got)
	}
	v.candMu.Lock()
	defer v.candMu.Unlock()
	if v.cands["o/r"] == nil || len(v.cands["o/r"].users) == 0 {
		t.Error("users should stay cached when only the teams failed")
	}
}

func TestReviewerPickingATeamRequestsOrgSlug(t *testing.T) {
	log := fakeGH(t, false)
	_, m := reviewerView(t)
	cmd := m.Match("sre")[0].Run()
	if cmd == nil {
		t.Fatal("picking should run gh")
	}
	cmd()
	b, _ := os.ReadFile(log)
	if want := "pr edit 7 -R o/r --add-reviewer o/sre"; !strings.Contains(string(b), want) {
		t.Errorf("gh got %q, want %q", b, want)
	}
}

func TestReviewerMenuPendingUntilTheFetchLands(t *testing.T) {
	fakeGH(t, false)
	v := mergeView(t, mergeable(7))
	prep := v.PrepareCommands()
	var m *ui.Menu
	for _, c := range v.Commands() {
		if c.ID == "add-reviewer" {
			m = c.Next()
		}
	}
	if !m.Pending() {
		t.Error("not pending before the fetch ran")
	}
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		if b, ok := c().(tea.BatchMsg); ok {
			for _, c := range b {
				run(c)
			}
		}
	}
	run(prep)
	if m.Pending() {
		t.Error("still pending after the fetch")
	}
}

func TestReviewerFetchWarnsWhenCapped(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*assignableUsers*) echo '{"data":{"repository":{"assignableUsers":{"pageInfo":{"hasNextPage":true,"endCursor":"c"},"nodes":[{"login":"ana"}]}}}}' ;;
*) echo '[]' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	v := mergeView(t, mergeable(7))
	var warned bool
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				run(c)
			}
		case ui.StatusMsg:
			warned = warned || strings.Contains(msg.Summary, "first 1000")
		}
	}
	run(v.PrepareCommands())
	if !warned {
		t.Error("a capped reviewer list was not reported")
	}
}
