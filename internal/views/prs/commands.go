package prs

import (
	"fmt"
	"os/exec"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

func (v *View) PrepareCommands() tea.Cmd { return nil }

// Commands implements ui.Commander: what the review popup offers, plus the
// branch and draft actions, on the selected PR. The verdicts and merge go
// through the popup so confirmations and the comment prompt are the same.
func (v *View) Commands() []ui.Command {
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	name := fmt.Sprintf("%s#%d", p.repo(), p.Number)
	popup := func(label string) func() tea.Cmd {
		return func() tea.Cmd {
			v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number}
			return v.activateReviewOption(label)
		}
	}
	gh := func(what string, args ...string) func() tea.Cmd {
		return func() tea.Cmd {
			v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number}
			return v.submitGh(what, args...)
		}
	}
	num, repo := strconv.Itoa(p.Number), p.repo()
	cmds := []ui.Command{
		{ID: "approve", Title: "Approve", Run: popup("Approve")},
		{ID: "comment", Title: "Comment", Run: popup("Comment")},
		{ID: "request-changes", Title: "Request changes", Run: popup("Request changes")},
	}
	if v.cfg.Merge {
		cmds = append(cmds,
			ui.Command{ID: "merge", Title: "Merge", Run: popup(mergeLabel)},
			ui.Command{ID: "auto-merge", Title: "Enable auto-merge", Run: popup(autoLabel)})
	}
	cmds = append(cmds, ui.Command{ID: "rebase", Title: "Rebase onto base branch",
		Run: gh("rebased "+name, "pr", "update-branch", num, "-R", repo, "--rebase")})
	if p.IsDraft {
		cmds = append(cmds, ui.Command{ID: "ready", Title: "Mark ready for review",
			Run: gh(name+" is ready for review", "pr", "ready", num, "-R", repo)})
	} else {
		cmds = append(cmds, ui.Command{ID: "draft", Title: "Mark as draft",
			Run: gh(name+" is a draft", "pr", "ready", num, "-R", repo, "--undo")})
	}
	cmds = append(cmds, ui.Command{ID: "close", Title: "Close with comment", Run: func() tea.Cmd {
		v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number, verdict: "close"}
		return nil
	}})
	cmds = append(cmds, ui.Command{ID: "add-reviewer", Title: "Add reviewer", Run: func() tea.Cmd {
		v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number, verdict: "add-reviewer"}
		return nil
	}})
	for _, mention := range p.reviewers() {
		who := mention[1:] // a team stays owner/team, which gh accepts as is
		cmds = append(cmds, ui.Command{ID: "remove-reviewer-" + who, Title: "Remove reviewer: " + who,
			Run: gh("removed "+who+" from "+name, "pr", "edit", num, "-R", repo, "--remove-reviewer", who)})
	}
	for i := range cmds {
		cmds[i].Detail = name
	}
	return cmds
}

// submitGh runs one gh command on the popup's PR; its outcome comes back
// as a reviewDoneMsg without a verdict.
func (v *View) submitGh(what string, args ...string) tea.Cmd {
	v.review.submitting, v.review.action = true, "gh"
	url := v.review.url
	return func() tea.Msg {
		if out, err := exec.Command("gh", args...).CombinedOutput(); err != nil {
			return reviewDoneMsg{from: v, what: "gh pr " + args[1] + " failed", err: ghErr(err, out)}
		}
		return reviewDoneMsg{from: v, what: what, url: url}
	}
}
