package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	// kindNum is a whole number, edited as text but validated as an int so
	// a duration-shaped value cannot be stored in a count.
	kindNum
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

// sortSetting is a view's startup sort. "default" means the view's own,
// which is also what an empty config value means, so the row reads the
// same as the file.
func sortSetting(path string, names []string, get func(config.Config) string, set func(*config.Config, string)) setting {
	return setting{
		label: "sort on open", path: path, kind: kindEnum, note: "restart",
		options: func() []string { return append([]string{"default"}, names...) },
		get: func(c config.Config) string {
			if v := get(c); v != "" {
				return v
			}
			return "default"
		},
		set: func(c *config.Config, v string) {
			if v == "default" {
				v = ""
			}
			set(c, v)
		},
	}
}

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

// numSetting is a whole-number field, edited as text and ignored when the
// input is not a number (the overlay validates before calling set).
func numSetting(label, path, note string, get func(config.Config) int, set func(*config.Config, int)) setting {
	return setting{
		label: label, path: path, kind: kindNum, note: note,
		get: func(c config.Config) string { return strconv.Itoa(get(c)) },
		set: func(c *config.Config, v string) {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return
			}
			set(c, n)
		},
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
		boolSetting("hotkey bar", "footer", "",
			func(c config.Config) bool { return c.FooterEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.Footer, v) }),
		boolSetting("nerd font glyphs", "theme.glyphs", "",
			func(c config.Config) bool { return c.GlyphsEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.Theme.Glyphs, v) }),
		header("Keybinds"),
		{
			label: "edit keybinds", path: "action:edit_keybinds", kind: kindAction,
			get: func(config.Config) string { return "" },
			set: func(*config.Config, string) {},
		},
		header("Behavior"),
		boolSetting("mark new items", "unread", "",
			func(c config.Config) bool { return c.UnreadEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.Unread, v) }),
		boolSetting("sync reads with GitHub", "unread_sync", "",
			func(c config.Config) bool { return c.UnreadSync },
			func(c *config.Config, v bool) { c.UnreadSync = v }),
		boolSetting("group by sort", "grouping", "",
			func(c config.Config) bool { return c.Grouping },
			func(c *config.Config, v bool) { c.Grouping = v }),
		boolSetting("check for updates", "update_check", "restart",
			func(c config.Config) bool { return c.UpdateCheckEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.UpdateCheck, v) }),
		{
			label: "toggle behavior", path: "toggles", kind: kindEnum,
			options: func() []string { return []string{"ephemeral", "persist"} },
			get: func(c config.Config) string {
				if c.TogglesPersist() {
					return "persist"
				}
				return "ephemeral"
			},
			set: func(c *config.Config, v string) { c.Toggles = v },
		},
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
			label: "notification click", path: "notifications.click", kind: kindEnum, note: "restart",
			options: func() []string { return []string{"url", "none"} },
			get:     func(c config.Config) string { return c.Notify.ClickAction() },
			set:     func(c *config.Config, v string) { c.Notify.Click = v },
		},
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
		{
			label: "search filter", path: "github.filter", kind: kindText,
			note: "restart",
			get:  func(c config.Config) string { return c.GitHub.Filter },
			set:  func(c *config.Config, v string) { c.GitHub.Filter = v },
		},
		{
			label: "review search filter", path: "github.review_filter",
			kind: kindText, note: "restart",
			get: func(c config.Config) string { return c.GitHub.ReviewFilter },
			set: func(c *config.Config, v string) { c.GitHub.ReviewFilter = v },
		},
		{
			label: ui.Glyph(ui.IconReset, "") + " reset filters",
			path:  "action:reset_filters", kind: kindAction,
			get: func(config.Config) string { return "" },
			set: func(*config.Config, string) {},
		},
		sortSetting("github.sort", config.PRSortNames,
			func(c config.Config) string { return c.GitHub.Sort },
			func(c *config.Config, v string) { c.GitHub.Sort = v }),
		boolSetting("reverse sort", "github.reverse", "restart",
			func(c config.Config) bool { return c.GitHub.Reverse },
			func(c *config.Config, v bool) { c.GitHub.Reverse = v }),
		boolSetting("show review-requested", "github.show_review_requested", "restart",
			func(c config.Config) bool { return c.ShowReviewRequested() },
			func(c *config.Config, v bool) { setOptBool(&c.GitHub.ShowReviewRequested, v) }),
		boolSetting("lazy paging", "github.lazy_paging", "restart",
			func(c config.Config) bool { return c.GitHub.LazyPagingEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.GitHub.LazyPaging, v) }),
		numSetting("page size", "github.page_size", "restart",
			func(c config.Config) int { return c.GitHub.ResolvedPageSize() },
			func(c *config.Config, v int) { c.GitHub.PageSize = v }),
		numSetting("summary lines", "github.summary_lines", "restart",
			func(c config.Config) int { return c.GitHub.SummaryLines },
			func(c *config.Config, v int) { c.GitHub.SummaryLines = v }),
		boolSetting("mark reviewed PRs", "github.mark_reviewed", "restart",
			func(c config.Config) bool { return c.GitHub.MarkReviewed },
			func(c *config.Config, v bool) { c.GitHub.MarkReviewed = v }),
		boolSetting("hide approved PRs", "github.hide_approved", "restart",
			func(c config.Config) bool { return c.GitHub.HideApproved },
			func(c *config.Config, v bool) { c.GitHub.HideApproved = v }),
		boolSetting("refresh row on settle", "github.refresh_row", "restart",
			func(c config.Config) bool { return c.GitHub.RefreshRowEnabled() },
			func(c *config.Config, v bool) { setOptBool(&c.GitHub.RefreshRow, v) }),
		boolSetting("merge from review popup", "github.merge", "restart",
			func(c config.Config) bool { return c.GitHub.Merge },
			func(c *config.Config, v bool) { c.GitHub.Merge = v }),
		{
			label: "merge method", path: "github.merge_method", kind: kindEnum,
			options: func() []string { return []string{"squash", "merge", "rebase"} },
			get:     func(c config.Config) string { return c.GitHub.ResolvedMergeMethod() },
			set:     func(c *config.Config, v string) { c.GitHub.MergeMethod = v },
		},
		boolSetting("delete branch after merge", "github.merge_delete_branch", "restart",
			func(c config.Config) bool { return c.GitHub.MergeDeleteBranch },
			func(c *config.Config, v bool) { c.GitHub.MergeDeleteBranch = v }),
		boolSetting("inline diff pane", "github.diff_pane", "restart",
			func(c config.Config) bool { return c.GitHub.DiffPane },
			func(c *config.Config, v bool) { c.GitHub.DiffPane = v }),
		header("Linear"),
		sortSetting("linear.sort", config.LinearSortNames,
			func(c config.Config) string { return c.Linear.Sort },
			func(c *config.Config, v string) { c.Linear.Sort = v }),
		boolSetting("reverse sort", "linear.reverse", "restart",
			func(c config.Config) bool { return c.Linear.Reverse },
			func(c *config.Config, v bool) { c.Linear.Reverse = v }),
		header("Sessions"),
		sortSetting("sessions.sort", config.SessionsSortNames,
			func(c config.Config) string { return c.Sessions.Sort },
			func(c *config.Config, v string) { c.Sessions.Sort = v }),
		boolSetting("reverse sort", "sessions.reverse", "restart",
			func(c config.Config) bool { return c.Sessions.Reverse },
			func(c *config.Config, v bool) { c.Sessions.Reverse = v }),
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

// settingsTabs groups the table's sections into tabs, so the overlay is a
// few screenfuls to page between rather than one long scroll. Each entry
// lists the section headers it holds, in table order; a section missing
// from every tab would be unreachable, which a test checks.
var settingsTabs = []struct {
	name     string
	sections []string
}{
	{"general", []string{"Behavior", "Views", "Keybinds"}},
	{"appearance", []string{"Theme"}},
	{"alerts", []string{"Auto-refresh", "Notifications"}},
	{"prs", []string{"PRs"}},
	{"linear", []string{"Linear", "Linear filter"}},
	{"sessions", []string{"Sessions"}},
}

// overlayWidth fixes the panel's inner width so it does not resize as you
// move between tabs: a box that changes size under the cursor reads as the
// whole panel jumping. Wide enough for the longest row (the config path)
// and every tab's labels and values.
const (
	overlayWidth = 72
	// The border (2) and horizontal padding (4) that Width() counts, so the
	// rule and the hint can be sized to the actual text area.
	overlayChrome  = 6
	overlayContent = overlayWidth - overlayChrome
)

// TopY is the overlay's top row: where the tallest tab would sit if it
// were centered, so the panel holds still and grows downward instead of
// re-centering itself every time you switch tabs.
func (o *configOverlay) TopY(cfg config.Config, screenH int) int {
	// Measure the real render rather than recomputing its arithmetic: a
	// duplicate formula was off by one on the single-section tabs, which
	// print no header.
	saved := o.tab
	tallest := 0
	for i := range settingsTabs {
		o.tab = i
		if h := lipgloss.Height(o.View(cfg)); h > tallest {
			tallest = h
		}
	}
	o.tab = saved
	if tallest >= screenH {
		return 0 // taller than the screen: start at the top, not above it
	}
	return max(0, (screenH-tallest)/2)
}

// Rows above the first setting inside the box: the border, the title, a
// blank, the tab bar, the rule, and a blank. Derived from View's own
// preamble, and a test renders the overlay to confirm they agree.
const settingsPreamble = 6

// RowAt maps a click inside the overlay to a settings row, given the box's
// top-left corner. Returns -1 for a click on the chrome, a header, or a
// blank line between sections.
func (o *configOverlay) RowAt(boxX, boxY, x, y int) int {
	line := y - boxY - settingsPreamble
	if line < 0 {
		return -1
	}
	// Walk the visible rows the way View prints them, counting the blank
	// line a section header puts before it (bar the first).
	vis := o.visible()
	sections := 0
	for _, idx := range vis {
		if o.rows[idx].kind == kindHeader {
			sections++
		}
	}
	printHeaders := sections >= 2

	at := 0
	for n, idx := range vis {
		if o.rows[idx].kind == kindHeader {
			if !printHeaders {
				continue
			}
			if n > 0 {
				at++ // the blank line before the header
			}
			at++ // the header itself
			continue
		}
		if at == line {
			return idx
		}
		at++
	}
	return -1
}

// TabAt maps a click on the tab bar to a tab index, or -1. The tab bar is
// the fourth row inside the box (border, title, blank, tabs).
func (o *configOverlay) TabAt(boxX, boxY, x, y int) int {
	if y-boxY != 3 {
		return -1
	}
	// The bar starts after the border and the left padding.
	col := x - boxX - 3
	at := 0
	for i, t := range settingsTabs {
		w := lipgloss.Width(t.name)
		if col >= at && col < at+w {
			return i
		}
		at += w + 2 // the two spaces between tabs
	}
	return -1
}

// SetCursor moves to a row, ignoring headers and anything out of range.
func (o *configOverlay) SetCursor(i int) {
	if i < 0 || i >= len(o.rows) || o.rows[i].kind == kindHeader {
		return
	}
	o.cursor, o.errMsg = i, ""
}

// SetTabIndex switches to a tab by index.
func (o *configOverlay) SetTabIndex(i int) {
	if i < 0 || i >= len(settingsTabs) || i == o.tab {
		return
	}
	o.tab = i
	o.editing, o.buf, o.errMsg = false, "", ""
	o.cursor = o.firstSetting()
}

// Size is the overlay's rendered width and height, for hit-testing.
func (o *configOverlay) Size(cfg config.Config) (w, h int) {
	v := o.View(cfg)
	return lipgloss.Width(v), lipgloss.Height(v)
}

// tabBar renders the section tabs, the active one highlighted.
func (o *configOverlay) tabBar() string {
	var parts []string
	for i, t := range settingsTabs {
		if i == o.tab {
			parts = append(parts, ui.Accent.Bold(true).Render(t.name))
			continue
		}
		parts = append(parts, ui.Dim.Render(t.name))
	}
	return strings.Join(parts, "  ")
}

// configOverlay is the ctrl+s modal: a cursor over one tab's rows.
type configOverlay struct {
	rows    []setting
	tab     int
	cursor  int // an index into o.rows, always within the active tab
	editing bool
	buf     string
	errMsg  string
}

func newConfigOverlay() *configOverlay {
	o := &configOverlay{rows: settingsTable()}
	o.cursor = o.firstSetting()
	return o
}

// firstSetting is the active tab's first non-header row.
func (o *configOverlay) firstSetting() int {
	for _, idx := range o.visible() {
		if o.rows[idx].kind != kindHeader {
			return idx
		}
	}
	return 0
}

// visible lists the table indices the active tab shows, in table order.
// Collected rather than sliced: a tab's sections need not be adjacent in
// the table, and assuming they were left one section unreachable.
func (o *configOverlay) visible() []int {
	var out []int
	// Section order follows the tab's own list, not the table's: the tab
	// decides what reads first, and the table's order is incidental.
	for _, want := range settingsTabs[o.tab].sections {
		show := false
		for i, s := range o.rows {
			if s.kind == kindHeader {
				show = s.label == want
			}
			if show {
				out = append(out, i)
			}
		}
	}
	return out
}

// inTab reports whether a section header belongs to the active tab.
func (o *configOverlay) inTab(section string) bool {
	for _, want := range settingsTabs[o.tab].sections {
		if want == section {
			return true
		}
	}
	return false
}

// setTab switches tabs and puts the cursor on that tab's first setting.
func (o *configOverlay) setTab(d int) {
	o.tab = (o.tab + d + len(settingsTabs)) % len(settingsTabs)
	o.editing, o.buf, o.errMsg = false, "", ""
	o.cursor = o.firstSetting()
}

// next returns the nearest selectable (non-header) row from i in direction d,
// or i's clamp when there is none.
func (o *configOverlay) next(i, d int) int {
	vis := o.visible()
	// Walk the visible indices, not the table: the tab's rows may not be
	// one contiguous run.
	at := -1
	for k, idx := range vis {
		if idx == i {
			at = k
			break
		}
	}
	for k := at + d; k >= 0 && k < len(vis); k += d {
		if o.rows[vis[k]].kind != kindHeader {
			return vis[k]
		}
	}
	if at >= 0 {
		return i // no further setting this way: stay put
	}
	for _, idx := range vis {
		if o.rows[idx].kind != kindHeader {
			return idx
		}
	}
	return 0
}

// Update handles one key. It returns a committed change (nil for pure
// navigation) and whether the overlay closed.
func (o *configOverlay) Update(msg tea.KeyMsg, cfg config.Config) (*settingChange, bool) {
	s := &o.rows[o.cursor]

	if o.editing {
		switch msg.String() {
		case "esc":
			o.editing, o.buf, o.errMsg = false, "", ""
		case "tab", "shift+tab":
			// Leave the edit behind rather than typing the key into it: a
			// half-typed value must not follow the cursor to another tab.
			d := +1
			if msg.String() == "shift+tab" {
				d = -1
			}
			o.setTab(d)
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
	case "tab":
		o.setTab(+1)
	case "shift+tab":
		o.setTab(-1)
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
		case kindText, kindNum:
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
	vis := o.visible()
	labelW := 0
	for _, idx := range vis {
		if s := o.rows[idx]; s.kind != kindHeader && len(s.label) > labelW {
			labelW = len(s.label)
		}
	}

	var b strings.Builder
	b.WriteString(ui.Bold.Render("settings"))
	b.WriteString("\n\n")
	b.WriteString(o.tabBar())
	b.WriteString("\n")
	// A rule under the tabs, full width: it separates the sections from the
	// rows, and the fixed width stops the box resizing per tab, which made
	// switching tabs feel like the panel jumped.
	b.WriteString(ui.Dim.Render(strings.Repeat("─", overlayContent)))
	b.WriteString("\n\n")
	// A tab holding one section needs no header: the tab name already says
	// it, and repeating it costs a row for nothing.
	sections := 0
	for _, idx := range vis {
		if o.rows[idx].kind == kindHeader {
			sections++
		}
	}
	for n, idx := range vis {
		i, s := idx, o.rows[idx]
		if s.kind == kindHeader {
			if sections < 2 {
				continue
			}
			if n > 0 {
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
		// One row per setting: a value long enough to wrap (a search
		// filter, say) would shift every row below it, and the mouse
		// hit-test counts rows.
		if w := lipgloss.Width(line); w > overlayContent {
			line = ansi.Truncate(line, overlayContent, "…")
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
	b.WriteString(ui.Dim.Render("↑↓ move · tab section · ←→ change · enter edit · esc close"))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Width(overlayWidth).
		Render(b.String())
}
