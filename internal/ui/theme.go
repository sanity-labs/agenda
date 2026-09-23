package ui

import (
	"fmt"
	"sort"

	"charm.land/lipgloss/v2"
)

// Palette is the color vocabulary everything draws from. Values are lipgloss
// color strings: ANSI numbers ("13") for the default terminal palette, hex
// ("#cba6f7") for the built-in themes and manual overrides.
type Palette struct {
	Accent  string // selection bar, active tab, matched-rune highlight, modal borders
	Border  string // pane and chrome borders
	Text    string // primary text (row titles)
	Dim     string // secondary text, separators, disabled
	Green   string
	Red     string
	Yellow  string
	Blue    string
	Cyan    string
	Magenta string
}

// builtins are the selectable palettes. "default" is the terminal's own ANSI
// colors, matching agenda's original hardcoded styling.
var builtins = map[string]Palette{
	"default": {
		Accent: "13", Border: "8", Text: "7", Dim: "8",
		Green: "2", Red: "1", Yellow: "3", Blue: "4", Cyan: "6", Magenta: "5",
	},
	"catppuccin-mocha": {
		Accent: "#cba6f7", Border: "#585b70", Text: "#cdd6f4", Dim: "#7f849c",
		Green: "#a6e3a1", Red: "#f38ba8", Yellow: "#f9e2af", Blue: "#89b4fa",
		Cyan: "#89dceb", Magenta: "#cba6f7",
	},
	"catppuccin-latte": {
		Accent: "#8839ef", Border: "#acb0be", Text: "#4c4f69", Dim: "#9ca0b0",
		Green: "#40a02b", Red: "#d20f39", Yellow: "#df8e1d", Blue: "#1e66f5",
		Cyan: "#04a5e5", Magenta: "#8839ef",
	},
	"tokyonight": {
		Accent: "#bb9af7", Border: "#3b4261", Text: "#c0caf5", Dim: "#565f89",
		Green: "#9ece6a", Red: "#f7768e", Yellow: "#e0af68", Blue: "#7aa2f7",
		Cyan: "#7dcfff", Magenta: "#bb9af7",
	},
	"gruvbox": {
		Accent: "#fe8019", Border: "#504945", Text: "#ebdbb2", Dim: "#928374",
		Green: "#b8bb26", Red: "#fb4934", Yellow: "#fabd2f", Blue: "#83a598",
		Cyan: "#8ec07c", Magenta: "#d3869b",
	},
	"dracula": {
		Accent: "#ff79c6", Border: "#44475a", Text: "#f8f8f2", Dim: "#6272a4",
		Green: "#50fa7b", Red: "#ff5555", Yellow: "#f1fa8c", Blue: "#bd93f9",
		Cyan: "#8be9fd", Magenta: "#ff79c6",
	},
	"nord": {
		Accent: "#88c0d0", Border: "#434c5e", Text: "#d8dee9", Dim: "#616e88",
		Green: "#a3be8c", Red: "#bf616e", Yellow: "#ebcb8b", Blue: "#81a1c1",
		Cyan: "#88c0d0", Magenta: "#b48ead",
	},
	"rose-pine": {
		Accent: "#c4a7e7", Border: "#403d52", Text: "#e0def4", Dim: "#6e6a86",
		Green: "#31748f", Red: "#eb6f92", Yellow: "#f6c177", Blue: "#9ccfd8",
		Cyan: "#ebbcba", Magenta: "#c4a7e7",
	},
}

// PaletteNames lists the built-in palette names, "default" first, the rest
// alphabetical: the cycle order the config overlay uses.
func PaletteNames() []string {
	names := make([]string, 0, len(builtins))
	for n := range builtins {
		if n != "default" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return append([]string{"default"}, names...)
}

// ResolvePalette looks up a built-in by name ("" means default) and applies
// manual per-color overrides on top.
func ResolvePalette(name string, overrides map[string]string) (Palette, error) {
	if name == "" {
		name = "default"
	}
	p, ok := builtins[name]
	if !ok {
		return p, fmt.Errorf("unknown theme %q (built-ins: %v)", name, PaletteNames())
	}
	for k, v := range overrides {
		switch k {
		case "accent":
			p.Accent = v
		case "border":
			p.Border = v
		case "text":
			p.Text = v
		case "dim":
			p.Dim = v
		case "green":
			p.Green = v
		case "red":
			p.Red = v
		case "yellow":
			p.Yellow = v
		case "blue":
			p.Blue = v
		case "cyan":
			p.Cyan = v
		case "magenta":
			p.Magenta = v
		default:
			return p, fmt.Errorf("unknown palette color %q", k)
		}
	}
	return p, nil
}

// The live styles everything renders with. SetPalette reassigns them; that
// happens before the Bubble Tea program starts (and, for live theme switching,
// inside the single-threaded update loop), so readers never race a write.
var (
	pal Palette

	Accent  lipgloss.Style
	Green   lipgloss.Style
	Red     lipgloss.Style
	Yellow  lipgloss.Style
	Blue    lipgloss.Style
	Cyan    lipgloss.Style
	Magenta lipgloss.Style
	Dim     lipgloss.Style
	Text    lipgloss.Style
	Bold    lipgloss.Style
	Faint   lipgloss.Style

	// Band is reverse video in the accent color: the accent as background
	// with the terminal's own background as the text color. Used for section
	// bands, which must not read as "a brighter row". The only other filled
	// background in a list is the yellow filter-match highlight, and that one
	// covers a few runes mid-row rather than a full-width line.
	Band lipgloss.Style
)

// Pal returns the current palette, for code that needs raw colors (borders,
// backgrounds) rather than a foreground style.
func Pal() Palette { return pal }

// paletteGen counts palette swaps. Include PaletteGen in any cache key of
// rendered output (memoized markdown bodies, cached renderers) so a live
// theme switch invalidates it.
var paletteGen int

func PaletteGen() int { return paletteGen }

// ToastMsg asks the root model to show an in-app notification toast.
type ToastMsg struct {
	Title, Body string
}

// glyphsOn gates the decorative Nerd Font icons (tab and nav-tree icons);
// the core status glyphs are always on, as upstream already requires a Nerd
// Font. Set from config before the program runs or in the update loop.
var glyphsOn = true

// SetGlyphs toggles them; the markdown renderer caches on PaletteGen, so a
// toggle must invalidate it too.
func SetGlyphs(on bool) {
	if glyphsOn != on {
		glyphsOn = on
		paletteGen++
	}
}

// Glyph returns icon (plus a trailing space) when decorative glyphs are on,
// else the fallback ("" hides it entirely).
func Glyph(icon, fallback string) string {
	if glyphsOn {
		return icon + " "
	}
	if fallback == "" {
		return ""
	}
	return fallback + " "
}

// Fg is a foreground style for an arbitrary lipgloss color string.
func Fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

// SetPalette swaps the live palette. Safe only from main before the program
// runs or from within the update loop.
func SetPalette(p Palette) {
	pal = p
	paletteGen++
	Accent = Fg(p.Accent)
	Green = Fg(p.Green)
	Red = Fg(p.Red)
	Yellow = Fg(p.Yellow)
	Blue = Fg(p.Blue)
	Cyan = Fg(p.Cyan)
	Magenta = Fg(p.Magenta)
	Dim = Fg(p.Dim)
	Text = Fg(p.Text)
	Bold = lipgloss.NewStyle().Bold(true)
	Faint = lipgloss.NewStyle().Faint(true)
	// Reverse rather than an explicit Background/Foreground pair: the palette
	// has no "base" color to use for the text, and reverse video picks up
	// whatever background the terminal actually has, so the band stays
	// readable on both the light and the dark built-in themes.
	Band = Fg(p.Accent).Reverse(true).Bold(true)
}

func init() { SetPalette(builtins["default"]) }
