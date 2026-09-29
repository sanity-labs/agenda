package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// theme holds the styles shared across the chrome. Views may define their own
// row styling but should pull accent colors from here for consistency.
type theme struct {
	tabActive     lipgloss.Style
	tabInactive   lipgloss.Style
	tabBar        lipgloss.Style
	preview       lipgloss.Style
	previewZoomed lipgloss.Style
	previewFloat  lipgloss.Style
	footer        lipgloss.Style
	footerKey     lipgloss.Style
	footerDesc    lipgloss.Style
	footerSep     lipgloss.Style
}

// defaultTheme derives the chrome styles from the active ui palette; rebuilt
// when the palette changes so a live theme switch restyles the chrome too.
func defaultTheme() theme {
	var (
		accent = lipgloss.Color(ui.Pal().Accent)
		dim    = lipgloss.Color(ui.Pal().Dim)
		border = lipgloss.Color(ui.Pal().Border)
	)
	return theme{
		tabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(accent).
			Bold(true).
			Padding(0, 2),
		tabInactive: lipgloss.NewStyle().
			Foreground(dim).
			Padding(0, 2),
		tabBar: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(border).
			BorderBottom(true).
			BorderTop(false).BorderLeft(false).BorderRight(false),
		preview: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(border).
			BorderLeft(true).
			BorderTop(false).BorderBottom(false).BorderRight(false).
			PaddingLeft(2),
		previewZoomed: lipgloss.NewStyle().
			PaddingLeft(2),
		previewFloat: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1),
		footer: lipgloss.NewStyle().
			Foreground(dim),
		footerKey: lipgloss.NewStyle().
			Foreground(accent).
			Bold(true),
		footerDesc: lipgloss.NewStyle().
			Foreground(dim),
		footerSep: lipgloss.NewStyle().
			Foreground(dim).
			SetString(" · "),
	}
}
