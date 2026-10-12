package ui

import (
	"bytes"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/x/ansi"
)

// Markdown rendering for the preview panes, shared by every view. One style:
// glamour's dark config as the baseline (so body text and syntax colors stay
// familiar), with a few legibility upgrades on top: heading icons, glyph
// checkboxes, a gutter bar marking code blocks, and inline code recolored
// for contrast (the stock red-on-grey is hard to read).

// mdStyle derives the glamour style config from the current palette.
func mdStyle() glamouransi.StyleConfig {
	p := Pal()
	s := styles.DarkStyleConfig

	// Headings: nerd-font level icons instead of ## prefixes (when the
	// glyphs toggle allows them); H1 gets an accent band either way.
	s.Heading.Color = new(p.Accent)
	s.H1.StylePrimitive = glamouransi.StylePrimitive{
		Prefix: " ", Suffix: " ",
		Color:           new("0"),
		BackgroundColor: new(p.Accent),
		Bold:            new(true),
	}
	if glyphsOn {
		s.H1.Prefix = " 󰲡 "
		s.H2.Prefix = "󰲣 "
		s.H3.Prefix = "󰲥 "
		s.H4.Prefix = "󰲧 "
		s.H5.Prefix = "󰲩 "
		s.H6.StylePrimitive = glamouransi.StylePrimitive{Prefix: "󰲫 ", Color: new(p.Dim)}
	} else {
		s.H6.Color = new(p.Dim)
	}

	// Blockquote: a bar gutter with dimmed italic text.
	s.BlockQuote.Color = new(p.Dim)
	s.BlockQuote.Italic = new(true)
	s.BlockQuote.IndentToken = new("▐ ")

	if glyphsOn {
		s.Task.Ticked = Green.Render("󰱒") + " "
		s.Task.Unticked = Dim.Render("󰄱") + " "
	}

	s.Link = glamouransi.StylePrimitive{Color: new(p.Blue), Underline: new(true)}
	s.LinkText = glamouransi.StylePrimitive{Color: new(p.Cyan), Bold: new(true)}

	// Inline code: yellow reads clearly on the subtle border-grey in every
	// built-in palette, unlike the stock red.
	s.Code.Color = new(p.Yellow)
	s.Code.BackgroundColor = new(p.Border)

	s.HorizontalRule = glamouransi.StylePrimitive{
		Color:  new(p.Border),
		Format: "\n" + strings.Repeat("─", 24) + "\n",
	}

	return s
}

// mdCache reuses one renderer per (width, palette); building a renderer is
// far more expensive than rendering.
var mdCache struct {
	width, gen int
	r          *glamour.TermRenderer
}

// Markdown renders src for a preview pane of the given content width. Fenced
// code blocks are rendered by renderCodeBlock (glamour ignores IndentToken
// for code and its chroma theme registration is global and first-wins, so a
// block marker can't be styled through it); everything else goes through
// glamour. On any renderer error it falls back to the raw text; a preview
// should never be empty because of a markdown corner case.
func Markdown(src string, width int) string {
	width = max(20, width)
	if mdCache.r == nil || mdCache.width != width || mdCache.gen != PaletteGen() {
		r, err := glamour.NewTermRenderer(
			glamour.WithStyles(mdStyle()),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return src
		}
		mdCache.r, mdCache.width, mdCache.gen = r, width, PaletteGen()
	}

	var out []string
	for _, seg := range splitFences(src) {
		if seg.code {
			out = append(out, renderCodeBlock(seg.text, seg.lang, width))
			continue
		}
		if strings.TrimSpace(seg.text) == "" {
			continue
		}
		rendered, err := mdCache.r.Render(seg.text)
		if err != nil {
			rendered = seg.text
		}
		out = append(out, strings.Trim(rendered, "\n"))
	}
	return strings.Join(out, "\n\n")
}

// segment is a run of markdown text or one fenced code block's contents.
type segment struct {
	code bool
	lang string
	text string
}

// splitFences separates ```-fenced code blocks from the surrounding
// markdown, so code can render through our own block renderer. Fences may be
// indented (e.g. inside lists); an unterminated fence runs to the end.
func splitFences(src string) []segment {
	var segs []segment
	var buf []string
	var code bool
	var lang string
	flush := func() {
		if len(buf) > 0 || code {
			segs = append(segs, segment{code: code, lang: lang, text: strings.Join(buf, "\n")})
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			if code {
				flush()
				code, lang = false, ""
			} else {
				flush()
				code = true
				lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return segs
}

// chromaOnce force-registers glamour's "charm" chroma theme by rendering a
// throwaway snippet with the stock dark style. The registration is global
// and first-wins inside glamour, so doing it here keeps our block renderer
// and glamour's own colors identical.
var chromaOnce sync.Once

func ensureChromaTheme() {
	chromaOnce.Do(func() {
		r, err := glamour.NewTermRenderer(glamour.WithStyles(styles.DarkStyleConfig), glamour.WithWordWrap(20))
		if err == nil {
			_, _ = r.Render("```go\nx\n```")
		}
	})
}

// renderCodeBlock draws one fenced block: an accent gutter bar per line with
// chroma-highlighted code, truncated to the pane (code doesn't wrap well).
func renderCodeBlock(code, lang string, width int) string {
	ensureChromaTheme()
	var hl bytes.Buffer
	text := strings.TrimRight(code, "\n")
	if err := quick.Highlight(&hl, text, lang, "terminal256", "charm"); err != nil {
		hl.Reset()
		hl.WriteString(text)
	}
	bar := "  " + Dim.Render("▎") + " "
	var b strings.Builder
	for i, line := range strings.Split(strings.TrimRight(hl.String(), "\n"), "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(bar)
		b.WriteString(ansi.Truncate(strings.ReplaceAll(line, "\t", "    "), max(1, width-4), "…"))
	}
	return b.String()
}
