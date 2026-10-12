package prs

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func toastOf(t *testing.T, cmd tea.Cmd) *ui.ToastMsg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	var found *ui.ToastMsg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case tea.BatchMsg:
			for _, sub := range m {
				walk(sub)
			}
		case ui.ToastMsg:
			found = &m
		}
	}
	walk(cmd)
	return found
}

// When the row you were on leaves the list on a refresh, a toast says
// which PR and why, so the cursor landing on another PR does not pass for
// the same one. A refresh that keeps the row says nothing.
func TestDepartedRowIsAnnounced(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{HideApproved: true}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	a := pr{Number: 1, URL: "u1", Title: "a", State: "OPEN"}
	b := pr{Number: 2, URL: "u2", Title: "b", State: "OPEN"}
	a.Repository.NameWithOwner, b.Repository.NameWithOwner = "o/r", "o/r"
	v.Update(reviewListMsg{page: searchPage{prs: []pr{a, b}}})
	v.list.Select(matchURL("u2"))

	if toastOf(t, v.Update(reviewListMsg{page: searchPage{prs: []pr{a, b}}})) != nil {
		t.Error("a refresh that keeps the selected row toasted")
	}

	approved := b
	approved.ReviewDecision = "APPROVED"
	toast := toastOf(t, v.Update(reviewListMsg{page: searchPage{prs: []pr{a, approved}}}))
	if toast == nil || !strings.Contains(toast.Body, "o/r#2") || !strings.Contains(toast.Body, "hide_approved") {
		t.Errorf("approved-and-hidden row left without the right toast: %+v", toast)
	}

	v.list.Select(matchURL("u1"))
	toast = toastOf(t, v.Update(reviewListMsg{page: searchPage{prs: nil}}))
	if toast == nil || !strings.Contains(toast.Body, "no longer in the search") {
		t.Errorf("a row dropped by the search left without the right toast: %+v", toast)
	}
}
