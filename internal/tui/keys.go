package tui

import (
	"charm.land/bubbles/v2/key"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// globalKeys are handled by the root model regardless of the active view.
type globalKeys struct {
	NextView      key.Binding
	PrevView      key.Binding
	Refresh       key.Binding
	Quit          key.Binding
	Help          key.Binding
	Follow        key.Binding
	Filter        key.Binding
	Config        key.Binding
	Zoom          key.Binding
	TogglePreview key.Binding
	Messages      key.Binding
	PreviewUp     key.Binding
	PreviewDown   key.Binding
	PreviewPgUp   key.Binding
	PreviewPgDn   key.Binding
}

// newKeys resolves the global bindings from config overrides (scope "global"),
// falling back to the defaults.
func newKeys(km config.Keymap) globalKeys {
	bind := func(action, helpKey, desc string, def ...string) key.Binding {
		return ui.Bind(km.Of("global", action, def...), helpKey, desc)
	}
	g := globalKeys{
		NextView:      bind("next_view", "tab", "next view", "tab", "L"),
		PrevView:      bind("prev_view", "", "prev view", "shift+tab", "H"),
		Refresh:       bind("refresh", "", "refresh", "ctrl+r"),
		Quit:          bind("quit", "", "quit", "q", "ctrl+c"),
		Help:          bind("help", "", "help", "?"),
		Follow:        bind("follow", "", "related", "l"),
		Filter:        bind("filter", "", "filter", "f"),
		Config:        bind("config", "", "config", "ctrl+s"),
		Zoom:          bind("zoom", "", "zoom", "z"),
		TogglePreview: bind("toggle_preview", "", "preview", "v"),
		Messages:      bind("messages", "", "messages", "!"),
		PreviewUp:     bind("preview_up", "", "scroll preview", "shift+up"),
		PreviewDown:   bind("preview_down", "", "", "shift+down"),
		PreviewPgUp:   bind("preview_pgup", "", "", "pgup"),
		PreviewPgDn:   bind("preview_pgdn", "", "", "pgdown"),
	}
	// With the default up/down pair, show the combined glyph the README uses.
	if !km.Has("global", "preview_up") && !km.Has("global", "preview_down") {
		g.PreviewUp = key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("⇧↑↓", "scroll preview"))
	}
	return g
}
