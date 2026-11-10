package prs

import (
	"fmt"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/cache"
	"github.com/sanity-labs/agenda/internal/ui"
)

const (
	skimLabel = "reviewbot:skim"
	autoCache = "auto-approved"
)

// autoApproval is one PR the daemon approved. Entries are never dropped,
// so a restart cannot approve the same PR twice; PR is the row as it was,
// kept only while it may still linger in the list.
type autoApproval struct {
	At   time.Time `json:"at"`
	PR   *pr       `json:"pr,omitempty"`
	Seen bool      `json:"seen,omitempty"`
}

type autoApprovedMsg struct {
	from *View
	pr   pr
	err  error
}

// RunHeadless marks this view as the daemon's: only it auto-approves, so
// several windows on one account never approve the same PR twice.
func (v *View) RunHeadless() { v.headless = true }

func (p pr) hasLabel(name string) bool {
	for _, l := range p.Labels.Nodes {
		if l.Name == name {
			return true
		}
	}
	return false
}

// autoApprove approves the review-requested rows carrying the skim label:
// never your own, never a draft, never one you already approved, and
// each at most once.
func (v *View) autoApprove(page []pr) tea.Cmd {
	if !v.headless || !v.cfg.AutoApproveSkim || v.reviewsElsewhere {
		return nil
	}
	var cmds []tea.Cmd
	for _, p := range page {
		if _, done := v.auto[p.URL]; done || v.approving[p.URL] || p.ViewerDidAuthor ||
			p.IsDraft || p.State != "OPEN" || p.ViewerLatestReview.State == "APPROVED" ||
			p.repo() == "" || !p.hasLabel(skimLabel) {
			continue
		}
		if v.approving == nil {
			v.approving = map[string]bool{}
		}
		v.approving[p.URL] = true
		cmds = append(cmds, func() tea.Msg {
			out, err := ghCombined("pr", "review", strconv.Itoa(p.Number), "-R", p.repo(), "--approve")
			if err != nil {
				err = ghErr(err, out)
			}
			return autoApprovedMsg{from: v, pr: p, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (v *View) autoApproved(msg autoApprovedMsg) tea.Cmd {
	p := msg.pr
	name := fmt.Sprintf("%s#%d", p.repo(), p.Number)
	if msg.err != nil {
		return statusCmd(ui.SeverityWarn, fmt.Errorf("auto-approve %s: %w", name, msg.err))
	}
	p.ViewerLatestReview.State = "APPROVED"
	v.auto[p.URL] = autoApproval{At: time.Now(), PR: &p}
	v.saveAuto()
	v.applySort()
	title := "Auto-approved " + name
	cmds := []tea.Cmd{func() tea.Msg { return ui.Status(ui.SeverityInfo, "PRs", title, p.Title) }}
	if n := v.notifier; n != nil {
		cmds = append(cmds, func() tea.Msg { return n.Notify(title, p.Title, p.URL) })
	}
	return tea.Batch(cmds...)
}

// mergeAuto folds the cache file into v.auto: the daemon writes approvals
// and a window writes what it looked at, and neither may drop the other's.
func (v *View) mergeAuto() {
	saved, _ := cache.Load[map[string]autoApproval](autoCache)
	for url, a := range saved {
		if mine, ok := v.auto[url]; ok {
			mine.Seen = mine.Seen || a.Seen
			a = mine
		}
		v.auto[url] = a
	}
}

func (v *View) saveAuto() {
	v.mergeAuto()
	// The row on screen keeps its copy; the file has no use for it now.
	linger := v.cfg.ResolvedAutoApproveLinger()
	out := make(map[string]autoApproval, len(v.auto))
	for url, a := range v.auto {
		if a.Seen || time.Since(a.At) >= linger {
			a.PR = nil
		}
		out[url] = a
	}
	_ = cache.Save(autoCache, out)
}

// seeAuto expires an auto-approval once its detail has been on screen. The
// row stays while you are on it and goes when you move on or close it.
func (v *View) seeAuto(url string) {
	a, ok := v.auto[url]
	if !ok {
		return
	}
	if !a.Seen {
		a.Seen = true
		v.auto[url] = a
		v.saveAuto()
	}
	v.applySort()
}

// withAutoApproved tags the auto-approved rows and brings back the ones the
// approval took out of the search, for the linger window and until seen.
func (v *View) withAutoApproved(rev []pr) []pr {
	if len(v.auto) == 0 || v.reviewsElsewhere {
		return rev
	}
	sel := v.list.Selected().URL
	linger := v.cfg.ResolvedAutoApproveLinger()
	lingers := func(url string, a autoApproval) bool {
		return time.Since(a.At) < linger && (!a.Seen || (v.previewShown && url == sel))
	}
	shown := make(map[string]bool, len(rev))
	for i := range rev {
		shown[rev[i].URL] = true
		if a, ok := v.auto[rev[i].URL]; ok && lingers(rev[i].URL, a) {
			rev[i].AutoApproved = true
		}
	}
	added := false
	for url, a := range v.auto {
		if shown[url] || a.PR == nil || !lingers(url, a) {
			continue
		}
		p, _ := v.prByURL(url) // the live row when hide_approved hid it
		p.AutoApproved = true
		rev = append(rev, p)
		added = true
	}
	if added {
		rev = sortPRs(rev, v.sort, v.rev)
	}
	return rev
}
