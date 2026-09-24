package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The config overlay edits a fixed table of settings. Each setting knows its
// dotted config-file path, how to read/write itself on a live Config, and how
// it is edited (toggle, cycle, or typed text). Changes are written to the
// file immediately and applied live where possible; rows that only take
// effect on restart say so.

type settingKind int

const (
	kindHeader settingKind = iota
	kindBool
	kindEnum
	kindText
	// kindAction rows run something instead of storing a value (e.g. a test
	// notification); enter triggers them and nothing is written to the file.
	kindAction
)

type setting struct {
	label   string
	path    string // dotted path for config.Set; also keys live re-apply
	kind    settingKind
	note    string // extra hint, e.g. "restart"
	options func() []string
	get     func(c config.Config) string
	set     func(c *config.Config, v string)
}

func header(label string) setting { return setting{kind: kindHeader, label: label} }

func boolSetting(label, path, note string, get func(config.Config) bool, set func(*config.Config, bool)) setting {
	return setting{
		label: label, path: path, kind: kindBool, note: note,
		get: func(c config.Config) string {
			if get(c) {
				return "on"
			}
			return "off"
		},
		set: func(c *config.Config, v string) { set(c, v == "on") },
	}
}

// optBool resolves a default-true *bool flag.
func optBool(p *bool) bool { return p == nil || *p }

func setOptBool(p **bool, v bool) { *p = &v }

func durSetting(label, path string, get func(config.Config) string, set func(*config.Config, config.Duration)) setting {
	return setting{
		label: label, path: path, kind: kindText,
		get: get,
		set: func(c *config.Config, v string) {
			d, err := parseDur(v)
			if err != nil {
				return // the overlay validates before calling set
			}
			set(c, d)
		},
	}
}

func parseDur(s string) (config.Duration, error) {
	switch strings.TrimSpace(s) {
	case "", "0", "off", "none":
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("not a duration (try 5m, 90s, 0)")
	}
	return config.Duration(d), nil
}

func formatDur(d config.Duration) string {
	if d == 0 {
		return "0"
	}
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// durOverride renders a per-view refresh override: the global value applies
// when unset.
func durOverride(p *config.Duration) string {
	if p == nil {
		return "inherit"
	}
	return formatDur(*p)
}

func settingsTable() []setting {
	return []setting{
		header("Theme"),
		{
			label: "palette", path: "theme.name", kind: kindEnum,
			options: ui.PaletteNames,
			get: func(c config.Config) string {
				if c.Theme.Name == "" {
					return "default"
				}
				return c.Theme.Name
			},
			set: func(c *config.Config, v string) { c.Theme.Name = v },
		},
		boolSetting("nerd font glyphs", "theme.glyphs", "",
			func(c config.Config) bool { return c.GlyphsEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.Theme.Glyphs, v) }),
		header("Keybinds"),
		{
			label: "edit keybinds", path: "action:edit_keybinds", kind: kindAction,
			get: func(config.Config) string { return "" },
			set: func(*config.Config, string) {},
		},
		header("Lists"),
		boolSetting("group by sort", "grouping", "",
			func(c config.Config) bool { return c.Grouping },
			func(c *config.Config, v bool) { c.Grouping = v }),
		boolSetting("check for updates", "update_check", "restart",
			func(c config.Config) bool { return c.UpdateCheckEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.UpdateCheck, v) }),
		boolSetting("hide preview pane", "hide_preview", "",
			func(c config.Config) bool { return c.HidePreview },
			func(c *config.Config, v bool) { c.HidePreview = v }),
		header("Auto-refresh"),
		durSetting("every (global)", "refresh.every",
			func(c config.Config) string { return formatDur(c.Refresh.Every) },
			func(c *config.Config, d config.Duration) { c.Refresh.Every = d }),
		durSetting("prs", "refresh.prs",
			func(c config.Config) string { return durOverride(c.Refresh.PRs) },
			func(c *config.Config, d config.Duration) { c.Refresh.PRs = &d }),
		durSetting("linear", "refresh.linear",
			func(c config.Config) string { return durOverride(c.Refresh.Linear) },
			func(c *config.Config, d config.Duration) { c.Refresh.Linear = &d }),
		durSetting("sessions", "refresh.sessions",
			func(c config.Config) string { return durOverride(c.Refresh.Sessions) },
			func(c *config.Config, d config.Duration) { c.Refresh.Sessions = &d }),
		header("Notifications"),
		{
			label: "popup", path: "notifications.popup", kind: kindEnum, note: "restart",
			options: func() []string { return []string{"off", "terminal", "desktop"} },
			get: func(c config.Config) string {
				if c.Notify.Enabled() {
					return c.Notify.Popup
				}
				return "off"
			},
			set: func(c *config.Config, v string) { c.Notify.Popup = v },
		},
		boolSetting("sound", "notifications.sound", "restart",
			func(c config.Config) bool { return optBool(c.Notify.Sound) },
			func(c *config.Config, v bool) { setOptBool(&c.Notify.Sound, v) }),
		{
			label: "send test notification", path: "action:test_notification", kind: kindAction,
			get: func(config.Config) string { return "" },
			set: func(*config.Config, string) {},
		},
		header("Views"),
		boolSetting("prs", "github.enabled", "restart",
			func(c config.Config) bool { return optBool(c.GitHub.Enabled) },
			func(c *config.Config, v bool) { setOptBool(&c.GitHub.Enabled, v) }),
		boolSetting("sessions", "sessions.enabled", "restart",
			func(c config.Config) bool { return optBool(c.Sessions.Enabled) },
			func(c *config.Config, v bool) { setOptBool(&c.Sessions.Enabled, v) }),
		boolSetting("linear", "linear.enabled", "restart",
			func(c config.Config) bool { return optBool(c.Linear.Enabled) },
			func(c *config.Config, v bool) { setOptBool(&c.Linear.Enabled, v) }),
		header("PRs"),
		boolSetting("show review-requested", "github.show_review_requested", "restart",
			func(c config.Config) bool { return c.ShowReviewRequested() },
			func(c *config.Config, v bool) { setOptBool(&c.GitHub.ShowReviewRequested, v) }),
		boolSetting("mark reviewed PRs", "github.mark_reviewed", "restart",
			func(c config.Config) bool { return c.GitHub.MarkReviewed },
			func(c *config.Config, v bool) { c.GitHub.MarkReviewed = v }),
		boolSetting("inline diff pane", "github.diff_pane", "restart",
			func(c config.Config) bool { return c.GitHub.DiffPane },
			func(c *config.Config, v bool) { c.GitHub.DiffPane = v }),
		header("Linear filter"),
		boolSetting("include completed", "linear.filter.include_completed", "restart",
			func(c config.Config) bool { return c.Linear.Filter.IncludeCompleted },
			func(c *config.Config, v bool) { c.Linear.Filter.IncludeCompleted = v }),
		boolSetting("show comments", "linear.show_comments", "restart",
			func(c config.Config) bool { return c.Linear.ShowComments },
			func(c *config.Config, v bool) { c.Linear.ShowComments = v }),
		boolSetting("include canceled", "linear.filter.include_canceled", "restart",
			func(c config.Config) bool { return c.Linear.Filter.IncludeCanceled },
			func(c *config.Config, v bool) { c.Linear.Filter.IncludeCanceled = v }),
	}
}

// settingChange reports one committed edit: which setting, and its new
// display value ("on"/"off" for bools, the typed/cycled string otherwise).
type settingChange struct {
	s   *setting
	val string
}

// fileValue is what config.Set writes for this change.
func (sc settingChange) fileValue() any {
	if sc.s.kind == kindBool {
		return sc.val == "on"
	}
	return sc.val
}

// configOverlay is the ctrl+s modal: a cursor over the settings table.
type configOverlay struct {
	rows    []setting
	cursor  int
	editing bool
	buf     string
	errMsg  string
}

func newConfigOverlay() *configOverlay {
	o := &configOverlay{rows: settingsTable()}
	o.cursor = o.next(-1, +1) // first selectable row
	return o
}

// next returns the nearest selectable (non-header) row from i in direction d,
// or i's clamp when there is none.
func (o *configOverlay) next(i, d int) int {
	for j := i + d; j >= 0 && j < len(o.rows); j += d {
		if o.rows[j].kind != kindHeader {
			return j
		}
	}
	return max(0, min(i, len(o.rows)-1))
}

// Update handles one key. It returns a committed change (nil for pure
// navigation) and whether the overlay closed.
func (o *configOverlay) Update(msg tea.KeyMsg, cfg config.Config) (*settingChange, bool) {
	s := &o.rows[o.cursor]

	if o.editing {
		switch msg.String() {
		case "esc":
			o.editing, o.buf, o.errMsg = false, "", ""
		case "enter":
			if _, err := parseDur(o.buf); err != nil {
				o.errMsg = err.Error()
				return nil, false
			}
			o.editing, o.errMsg = false, ""
			val := strings.TrimSpace(o.buf)
			if val == "" {
				return nil, false // nothing typed: leave the setting alone
			}
			return &settingChange{s: s, val: val}, false
		case "backspace":
			if o.buf != "" {
				o.buf = o.buf[:len(o.buf)-1]
			}
		default:
			if kp, ok := tea.Msg(msg).(tea.KeyPressMsg); ok && kp.Text != "" {
				if r := []rune(kp.Text)[0]; r >= 0x20 && r != 0x7f {
					o.buf += kp.Text
				}
			}
		}
		return nil, false
	}

	switch msg.String() {
	case "esc", "q", "ctrl+s":
		return nil, true
	case "up", "k":
		o.cursor, o.errMsg = o.next(o.cursor, -1), ""
	case "down", "j":
		o.cursor, o.errMsg = o.next(o.cursor, +1), ""
	case "enter", "space", "right", "l":
		switch s.kind {
		case kindAction:
			if msg.String() == "enter" {
				return &settingChange{s: s}, false
			}
		case kindBool:
			val := "on"
			if s.get(cfg) == "on" {
				val = "off"
			}
			return &settingChange{s: s, val: val}, false
		case kindEnum:
			return &settingChange{s: s, val: cycle(s.options(), s.get(cfg), +1)}, false
		case kindText:
			if msg.String() == "enter" {
				o.editing, o.buf = true, ""
			}
		}
	case "left", "h":
		if s.kind == kindEnum {
			return &settingChange{s: s, val: cycle(s.options(), s.get(cfg), -1)}, false
		}
	}
	return nil, false
}

// cycle steps through opts from cur by d, wrapping.
func cycle(opts []string, cur string, d int) string {
	if len(opts) == 0 {
		return cur
	}
	at := 0
	for i, o := range opts {
		if o == cur {
			at = i
			break
		}
	}
	return opts[(at+d+len(opts))%len(opts)]
}

// View renders the overlay box.
func (o *configOverlay) View(cfg config.Config) string {
	labelW := 0
	for _, s := range o.rows {
		if s.kind != kindHeader && len(s.label) > labelW {
			labelW = len(s.label)
		}
	}

	var b strings.Builder
	b.WriteString(ui.Bold.Render("Config"))
	b.WriteString("\n\n")
	for i, s := range o.rows {
		if s.kind == kindHeader {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(ui.Dim.Render(s.label))
			b.WriteByte('\n')
			continue
		}
		cursor := "  "
		if i == o.cursor {
			cursor = ui.Accent.Render("› ")
		}
		val := s.get(cfg)
		if i == o.cursor && o.editing {
			val = o.buf + "█"
		}
		switch {
		case s.kind == kindAction:
			val = ui.Faint.Render("(enter)")
		case s.kind == kindEnum:
			val = "‹ " + val + " ›"
		case i == o.cursor:
			val = ui.Accent.Render(val)
		}
		line := fmt.Sprintf("%s%-*s  %s", cursor, labelW, s.label, val)
		if s.note != "" {
			line += ui.Faint.Render("  (" + s.note + ")")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	if o.errMsg != "" {
		b.WriteString(ui.Red.Render(o.errMsg))
		b.WriteByte('\n')
	}
	path, _ := config.Path()
	b.WriteString(ui.Faint.Render(path))
	b.WriteByte('\n')
	b.WriteString(ui.Dim.Render("↑↓ move · space/←→ change · enter edit · esc close"))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Render(b.String())
}
