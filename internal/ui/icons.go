package ui

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
	IconLinearIssue = "\U000f1591" // 󱖑 a Linear issue, next to its id
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
