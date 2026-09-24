package ui

// Status glyphs (Nerd Font). Shared so the PRs and Linear views render PR
// state/CI/review with the same vocabulary. Colors are applied by each view.

const (
	IconOpen      = "" //
	IconDraft     = "" //
	IconMerged    = "" //
	IconClosed    = "" //
	IconCIOK      = "" //
	IconCIFail    = "" //
	IconCIPending = "" //
	IconApproved  = "󰄜"
	IconChanges   = ""
	IconReviewReq = ""
	IconComment   = ""
	IconDot       = "·"
)

// Pill caps (powerline half-circles), used to round the ends of label pills.
// Both fall back to nothing when decorative glyphs are off, leaving the plain
// padded background the view rendered before.
const (
	IconPillLeft  = "\ue0b6" //
	IconPillRight = "\ue0b4" //
)

// Decorative icons, gated by the theme.glyphs toggle (see Glyph).
const (
	IconTabPRs      = "\ueb00" //  github
	IconTabSessions = "\uf120" //  terminal
	IconTabLinear   = "\uf4a0" //  linear
	IconNavMine     = "\uf007" //  user
	IconNavInbox    = "\uf01c" //  inbox
	IconNavAll      = "\uf0ca" //  list
	IconNavProject  = "\uf07b" //  folder
	IconStar        = "\uf005" //  star
	IconBell        = "\uf0f3" //  bell
)
