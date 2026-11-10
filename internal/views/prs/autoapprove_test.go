package prs

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

type recNotifier struct{ titles []string }

func (n *recNotifier) Notify(title, _, _ string) tea.Msg {
	n.titles = append(n.titles, title)
	return nil
}

// stubApprove records every gh call in place of running it.
func stubApprove(t *testing.T) *[][]string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := &[][]string{}
	prev := ghCombined
	ghCombined = func(args ...string) ([]byte, error) {
		*calls = append(*calls, args)
		return nil, nil
	}
	t.Cleanup(func() { ghCombined = prev })
	return calls
}

func skimPR(url string, num int) pr {
	p := mkPR(url, "bump deps", time.Hour)
	p.Number, p.State = num, "OPEN"
	p.Labels.Nodes = []label{{Name: "ci"}, {Name: skimLabel}}
	return p
}

// runAuto executes cmd the way the runtime would, feeding the view's own
// results back to it, and returns the message-log lines it produced.
func runAuto(v *View, cmd tea.Cmd) []ui.StatusMsg {
	var out []ui.StatusMsg
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
		case autoApprovedMsg:
			walk(v.Update(m))
		case ui.StatusMsg:
			out = append(out, m)
		}
	}
	walk(cmd)
	return out
}

func skimCfg() config.GitHubConfig { return config.GitHubConfig{AutoApproveSkim: true} }

func daemonView(cfg config.GitHubConfig, n *recNotifier) *View {
	v := NewReviews(cfg, nil, n, nil)
	v.RunHeadless()
	return v
}

func TestSkimLabelApprovesOnceFromTheDaemon(t *testing.T) {
	calls := stubApprove(t)
	plain := mkPR("u2", "real change", time.Hour)
	plain.State, plain.Number = "OPEN", 2
	page := reviewListMsg{page: searchPage{prs: []pr{skimPR("u1", 1), plain}}}

	window := NewReviews(skimCfg(), nil, nil, nil)
	runAuto(window, window.Update(page))
	if len(*calls) != 0 {
		t.Fatalf("a window approved: %v", *calls)
	}

	n := &recNotifier{}
	d := daemonView(skimCfg(), n)
	logged := runAuto(d, d.Update(page))
	want := "pr review 1 -R acme/repo --approve"
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != want {
		t.Fatalf("gh calls = %v, want one %q", *calls, want)
	}
	if len(n.titles) != 1 || len(logged) != 1 || !strings.Contains(logged[0].Summary, "acme/repo#1") {
		t.Errorf("notified %v, logged %v; want one of each", n.titles, logged)
	}

	// The search can still list it before the approval lands there.
	runAuto(d, d.Update(page))
	// A restarted daemon reads the record rather than approving again.
	d2 := daemonView(skimCfg(), &recNotifier{})
	runAuto(d2, d2.Update(page))
	if len(*calls) != 1 {
		t.Errorf("approved again: %v", *calls)
	}
}

func TestAutoApproveOffDoesNothing(t *testing.T) {
	calls := stubApprove(t)
	d := daemonView(config.GitHubConfig{}, &recNotifier{})
	runAuto(d, d.Update(reviewListMsg{page: searchPage{prs: []pr{skimPR("u1", 1)}}}))
	if len(*calls) != 0 {
		t.Errorf("approved with the key off: %v", *calls)
	}
}

func TestAutoApproveSkipsOwnApprovedAndDrafts(t *testing.T) {
	calls := stubApprove(t)
	own, approved, draft := skimPR("u1", 1), skimPR("u2", 2), skimPR("u3", 3)
	own.ViewerDidAuthor = true
	approved.ViewerLatestReview.State = "APPROVED"
	draft.IsDraft = true
	d := daemonView(skimCfg(), &recNotifier{})
	runAuto(d, d.Update(reviewListMsg{page: searchPage{prs: []pr{own, approved, draft}}}))
	if len(*calls) != 0 {
		t.Errorf("approved a PR it should have skipped: %v", *calls)
	}
}

func listed(v *View, url string) (pr, bool) {
	for _, p := range v.list.Items() {
		if p.URL == url {
			return p, true
		}
	}
	return pr{}, false
}

// An approval takes the PR out of review-requested:@me; the window keeps
// it, tagged, until its detail has been seen and the cursor moves on.
func TestAutoApprovedLingersUntilSeen(t *testing.T) {
	stubApprove(t)
	other := mkPR("u2", "other", 2*time.Hour)
	other.Number = 2
	d := daemonView(skimCfg(), &recNotifier{})
	runAuto(d, d.Update(reviewListMsg{page: searchPage{prs: []pr{skimPR("u1", 1), other}}}))

	w := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	w.SetSize(80, 60, 40)
	w.Update(ui.CacheChangedMsg{Name: autoCache})
	w.Update(reviewListMsg{page: searchPage{prs: []pr{other}}})
	p, ok := listed(w, "u1")
	if !ok || !p.AutoApproved {
		t.Fatalf("approved PR gone from the list (or untagged): %+v", p)
	}
	if w.list.Selected().URL != "u1" {
		t.Fatalf("selected %q, want the newer u1 on top", w.list.Selected().URL)
	}
	w.Update(ui.PreviewShownMsg(true))
	if _, ok := listed(w, "u1"); !ok {
		t.Fatal("the row vanished while its detail is on screen")
	}
	w.Update(tea.KeyPressMsg{Code: 'j'})
	if _, ok := listed(w, "u1"); ok {
		t.Error("a seen auto-approval stayed after the cursor moved on")
	}

	// Seen is recorded, so a restarted window does not bring it back.
	w2 := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	w2.Update(reviewListMsg{page: searchPage{prs: []pr{other}}})
	if _, ok := listed(w2, "u1"); ok {
		t.Error("a seen auto-approval came back after a restart")
	}
}

func TestAutoApprovedGoesWhenTheDetailCloses(t *testing.T) {
	stubApprove(t)
	d := daemonView(skimCfg(), &recNotifier{})
	runAuto(d, d.Update(reviewListMsg{page: searchPage{prs: []pr{skimPR("u1", 1)}}}))
	w := NewReviews(config.GitHubConfig{}, nil, nil, nil)
	w.Update(reviewListMsg{page: searchPage{}})
	w.Update(ui.PreviewShownMsg(true))
	w.Update(ui.PreviewShownMsg(false))
	if _, ok := listed(w, "u1"); ok {
		t.Error("a seen auto-approval stayed after the detail closed")
	}
}

func TestAutoApprovedStopsLingeringAfterTheWindow(t *testing.T) {
	stubApprove(t)
	linger := config.Duration(time.Hour)
	w := NewReviews(config.GitHubConfig{AutoApproveLinger: &linger}, nil, nil, nil)
	p := skimPR("u1", 1)
	w.auto["u1"] = autoApproval{At: time.Now().Add(-2 * time.Hour), PR: &p}
	w.Update(reviewListMsg{page: searchPage{}})
	if _, ok := listed(w, "u1"); ok {
		t.Error("an approval older than the linger window is still listed")
	}
}

func TestRenderAutoApprovedTag(t *testing.T) {
	p := skimPR("u1", 1)
	p.AutoApproved = true
	defer ui.SetGlyphs(ui.GlyphsOn())
	for _, glyphs := range []bool{true, false} {
		ui.SetGlyphs(glyphs)
		out := p.Render(80, false, ui.Highlighter{})
		t.Logf("glyphs %v:\n%s", glyphs, out)
		if !strings.Contains(out, "auto-approved") {
			t.Errorf("row missing the tag:\n%s", out)
		}
		if strings.Contains(out, ui.IconAutoApproved) != glyphs {
			t.Errorf("glyphs %v, icon shown %v", glyphs, !glyphs)
		}
	}
}
