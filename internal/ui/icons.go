package ui

import "strings"

// Status glyphs (Nerd Font). Shared so the PRs and Linear views render PR
// state/CI/review with the same vocabulary. Colors are applied by each view.

const (
	IconOpen      = "\uea64" // 
	IconDraft     = "\uebdb" // 
	IconMerged    = "\ueafe" //  git-merge (codicon, like open and draft)
	IconClosed    = "\uf48e" // 
	IconCIOK      = "\ueab2" // 
	IconCIFail    = "\uf467" // 
	IconCIPending = "\uf111" // 
	IconCISkipped = "\uf05e" // 
	IconApproved  = "\uedc6" // 
	IconChanges   = "\uf41b" // 
	IconReviewReq = "\uf4a0" // 
	IconComment   = "\uea6b" // 
	IconDot       = "\u00b7" // ·
)

// Linear issue status glyphs, one per workflow state type, coloured by
// IssueStatusIcon the way Linear colours them.
const (
	IconIssueBacklog    = "\U000f1978" // 󱥸
	IconIssueTodo       = "\uf4aa"     //
	IconIssueInProgress = "\U000f0aa1" // 󰪡
	IconIssueDone       = "\uf4a4"     //
	IconIssueCanceled   = "\uf530"     //
	IconIssueDuplicate  = "\U000f03e5" // 󰏥
	IconIssueTriage     = "\U000f0fe2" // 󰿢
)

// IssueStatusIcon is the coloured glyph for a Linear state type (backlog,
// unstarted, started, completed, canceled, triage); a canceled state named
// Duplicate gets its own. Unknown, as for an issue the token cannot read,
// is nothing: no glyph distracts less than a wrong one.
func IssueStatusIcon(stateType, stateName string) string {
	switch stateType {
	case "backlog":
		return IconIssueBacklog
	case "unstarted":
		return IconIssueTodo
	case "started":
		return Yellow.Render(IconIssueInProgress)
	case "completed":
		return Magenta.Render(IconIssueDone)
	case "canceled":
		if strings.Contains(strings.ToLower(stateName), "duplicate") {
			return Dim.Render(IconIssueDuplicate)
		}
		return Dim.Render(IconIssueCanceled)
	case "triage":
		return Fg("#f2994a").Render(IconIssueTriage)
	}
	return ""
}

// IconSection heads a preview section (Description, Checks, Comments).
const IconSection = "\uf0c9" //  list

// IconIssue marks a problem worth reading: warnings and errors in the
// message log, and the footer's pointer to it.
const IconIssue = "\uea6c" // 

// IconReset marks an action that puts a setting back to its default.
const IconReset = "\U000f099b" // 󰦛

// IconSearch heads the effective-query line.
const IconSearch = "\uf422" //  search

// IconUnread marks a row that arrived since the last fetch, until you
// select it.
const IconUnread = "\u25cf" // ●

// Pill caps (powerline half-circles), used to round the ends of label pills.
// Both fall back to nothing when decorative glyphs are off, leaving the plain
// padded background the view rendered before.
const (
	IconPillLeft  = "\ue0b6" // 
	IconPillRight = "\ue0b4" // 
)

// Decorative icons, gated by the theme.glyphs toggle (see Glyph).
const (
	IconTabReviews  = "\ueba1"     //
	IconTabPRs      = "\ueb00"     //  github
	IconTabSessions = "\uf120"     //  terminal
	IconTabLinear   = "\uf4a0"     //  linear
	IconLinearIssue = "\U000f1591" // 󱖑 a Linear issue
	IconNavMine     = "\uf007"     //  user
	IconNavInbox    = "\uf01c"     //  inbox
	IconNavAll      = "\uf0ca"     //  list
	IconNavProject  = "\uf07b"     //  folder
	IconStar        = "\uf005"     //  star
	IconBell        = "\uf0f3"     //  bell
)

// BlockHeader is a preview section's heading, the same in every view so
// the panes read alike. Glyph gated like the other decorative icons, so a
// plain-font setup gets the label without a tofu box.
func BlockHeader(name string) string { return Dim.Render(Glyph(IconSection, "") + name) }
