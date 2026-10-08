package ui

import (
	"fmt"
	"strings"

	"github.com/sanity-labs/agenda/internal/store"
)

// These ref builders live here so every view renders cross-references the same
// way: a PR shows status glyphs + title, an issue shows its title on a second
// line, a session shows its agent icon + a context snippet (see SessionRef).

// PRIcons renders state/CI/review/conflict glyphs for a stored PR. It returns
// "" when the PR's status is unknown (a zero record), so callers that only have
// a URL don't show a misleading "open" glyph.
func PRIcons(p store.PR) string {
	if p.State == "" {
		return ""
	}
	var parts []string
	switch p.State {
	case store.PRMerged:
		parts = append(parts, Magenta.Render(IconMerged))
	case store.PRClosed:
		parts = append(parts, Red.Render(IconClosed))
	case store.PRDraft:
		parts = append(parts, Dim.Render(IconDraft))
	default:
		parts = append(parts, Green.Render(IconOpen))
	}
	switch p.CI {
	case store.CIPassing:
		parts = append(parts, Green.Render(IconCIOK))
	case store.CIFailing:
		parts = append(parts, Red.Render(IconCIFail))
	case store.CIPending:
		parts = append(parts, Yellow.Render(IconCIPending))
	}
	switch p.Review {
	case store.ReviewApproved:
		parts = append(parts, Green.Render(IconApproved))
	case store.ReviewChanges:
		parts = append(parts, Red.Render(IconChanges))
	case store.ReviewPending:
		parts = append(parts, Yellow.Render(IconReviewReq))
	}
	if p.HasConflicts {
		parts = append(parts, Red.Render("⚠"))
	}
	return strings.Join(parts, " ")
}

// PRRef builds a PR cross-reference: status glyphs and the title on the main
// line, with repo#number as the dimmed detail. When the title is unknown the
// repo#number moves up to the main line (no redundant second line).
func PRRef(p store.PR, repo string, num int, title, url string) Ref {
	icons := PRIcons(p)
	repoNum := fmt.Sprintf("%s#%d", repo, num)
	ref := Ref{Kind: "pr", ID: url, URL: url}
	if title != "" {
		ref.Label = strings.TrimSpace(icons + "  " + Truncate(title, 60))
		ref.Detail = repoNum
	} else {
		ref.Label = strings.TrimSpace(icons + "  " + repoNum)
	}
	return ref
}

// ResolveRefsMsg asks whichever view owns Kind to bring these ids' metadata
// into the store: a PR's detail can then show the status of a ticket the
// Linear tab does not list. Sent by the view that needs it, answered by
// the owner publishing to the store.
type ResolveRefsMsg struct {
	Kind string
	IDs  []string
}

// IssueRef builds a Linear-issue cross-reference: the status glyph and the
// identifier on the main line, the issue title on the dimmed second line,
// as the PR detail lists them. url (when known) enables "open in browser"
// from the picker.
func IssueRef(id, title, url, status string) Ref {
	return Ref{Kind: "linear", ID: id, Label: status + " " + id, Detail: title, URL: url}
}
