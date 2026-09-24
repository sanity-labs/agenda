package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/notify"
	"github.com/sanity-labs/agenda/internal/ui"
)

const (
	tabBarHeight = 2 // tab labels + bottom border
	footerHeight = 1
	// Percent of width given to the preview pane. Two-line list rows give the
	// title its own line, so the list column can be narrower and the preview
	// gets the larger share.
	previewRatio = 50
)

// Model is agenda's root Bubble Tea model: chrome around a set of views.
type Model struct {
	cfg     config.Config
	keys    globalKeys
	theme   theme
	views   []View
	current int

	width, height int
	ready         bool

	// zoomed expands the preview pane to the full width (tmux-style zoom).
	zoomed bool
	// previewHidden drops the preview pane, giving the list the full width
	// (the toggle_preview key flips it; hide_preview sets the startup state).
	previewHidden bool
	// previewTransient marks a reveal that happened for an action (diff,
	// comments) rather than by the user's toggle; it re-hides when the
	// selection moves on or the action completes.
	previewTransient bool

	// preview scrolling, owned centrally so it works the same in every view.
	previewScroll int
	previewKey    string

	// cross-reference picker (nil unless the modal is open).
	picker     *ui.Picker
	pickerRefs []ui.Ref

	// field-scoped filter modal (nil unless open).
	filter *ui.FilterModal

	// config overlay (nil unless open).
	settings *configOverlay

	// keybind editor (nil unless open; reached from the config overlay).
	keysEd *keybindEditor

	// helpOpen shows the full-keymap overlay ('?').
	helpOpen bool

	// version is this build's version, shown at the right of the tab bar;
	// newer is set once a background check finds a newer release.
	version string
	newer   string

	// toast is the in-app notification popup (nil = none); toastGen ties
	// the auto-dismiss timer to the toast it was started for.
	toast    *ui.ToastMsg
	toastGen int

	// spinnerFrame advances the animation in tabs whose view is loading.
	spinnerFrame int

	// refresh holds each view's auto-refresh interval (0 = off), aligned
	// with views. refreshGen invalidates in-flight tick loops when the
	// intervals are edited live, so reconfiguring never doubles them up.
	refresh    []time.Duration
	refreshGen int
}

// spinnerTickMsg advances the tab spinner animation.
type spinnerTickMsg struct{}

// spinnerFrames is a braille spinner cycled while a view is fetching.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerTick() tea.Cmd {
	return tea.Tick(time.Second/12, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// anyLoading reports whether any view is currently fetching.
func (m Model) anyLoading() bool {
	for _, v := range m.views {
		if v.Loading() {
			return true
		}
	}
	return false
}

// New builds the root model from config. Views are constructed by the caller
// (main) and passed in, so the tui package doesn't import every view package.
func New(cfg config.Config, views []View) Model {
	return Model{
		cfg:           cfg,
		keys:          newKeys(cfg.Keys),
		previewHidden: cfg.HidePreview,
		theme:         defaultTheme(),
		views:         views,
		refresh:       refreshIntervals(cfg, views),
	}
}

// WithVersion sets the version shown in the tab bar.
func (m Model) WithVersion(v string) Model {
	m.version = v
	return m
}

// WithInitialView picks the tab shown at startup (a CLI argument, e.g.
// `agenda linear`).
func (m Model) WithInitialView(i int) Model {
	if i >= 0 && i < len(m.views) {
		m.current = i
	}
	return m
}

// refreshIntervals resolves each view's auto-refresh interval. The config
// names views by their lowercased tab title ("PRs" -> "prs").
func refreshIntervals(cfg config.Config, views []View) []time.Duration {
	out := make([]time.Duration, len(views))
	for i, v := range views {
		out[i] = cfg.RefreshFor(strings.ToLower(v.Title()))
	}
	return out
}

// refreshTickMsg fires one view's scheduled auto-refresh. Ticks from an older
// generation (before a live interval edit) are dropped without rescheduling.
type refreshTickMsg struct{ view, gen int }

func (m Model) refreshTick(i int) tea.Cmd {
	d := m.refresh[i]
	if d <= 0 {
		return nil
	}
	gen := m.refreshGen
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshTickMsg{view: i, gen: gen} })
}

func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2*len(m.views)+2)
	for i, v := range m.views {
		cmds = append(cmds, v.Init(), m.refreshTick(i))
	}
	// Views start flat; when grouping is configured on, a broadcast flips
	// them before the first data lands.
	if m.cfg.Grouping {
		cmds = append(cmds, groupingCmd(true))
	}
	// The views start out fetching, so kick the spinner loop; it stops itself
	// once nothing is loading.
	cmds = append(cmds, spinnerTick())
	if m.cfg.UpdateCheckEnabled() {
		cmds = append(cmds, checkUpdate(m.version))
	}
	return tea.Batch(cmds...)
}

// toastGoneMsg dismisses the toast it was scheduled for.
type toastGoneMsg struct{ gen int }

// groupingCmd emits the grouping toggle; the root model broadcasts non-key
// messages to every view.
func groupingCmd(on bool) tea.Cmd {
	return func() tea.Msg { return ui.GroupingMsg(on) }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.layout()
		return m, nil

	case spinnerTickMsg:
		// Keep the loop alive only while something is still loading.
		if !m.anyLoading() {
			return m, nil
		}
		m.spinnerFrame++
		return m, spinnerTick()

	case tea.MouseWheelMsg:
		// Hover-targeted wheel scroll: wheel over the list column scrolls the
		// list; wheel over the preview column scrolls the preview.
		if !m.ready {
			return m, nil
		}
		const step = 3
		var dir int
		switch msg.Button {
		case tea.MouseWheelUp:
			dir = -step
		case tea.MouseWheelDown:
			dir = step
		default:
			return m, nil // ignore horizontal wheel
		}
		listW, _, _ := m.dims()
		if msg.X >= listW {
			m.scrollPreview(dir)
		} else {
			m.scrollList(dir)
		}
		return m, nil

	case updateAvailableMsg:
		m.newer = string(msg)
		return m, nil
	case ui.RevealPreviewMsg:
		if m.previewHidden {
			m.previewHidden, m.previewTransient = false, true
			m.layout()
		}
		return m, nil
	case ui.ConcealPreviewMsg:
		if m.previewTransient {
			m.previewHidden, m.previewTransient = true, false
			m.layout()
		}
		return m, nil
	case ui.ToastMsg:
		m.toast = &msg
		m.toastGen++
		gen := m.toastGen
		return m, tea.Tick(6*time.Second, func(time.Time) tea.Msg { return toastGoneMsg{gen} })

	case toastGoneMsg:
		if msg.gen == m.toastGen {
			m.toast = nil
		}
		return m, nil

	case refreshTickMsg:
		if msg.view >= len(m.views) || msg.gen != m.refreshGen {
			return m, nil
		}
		// Always reschedule; skip the refetch when one is already in flight.
		if m.views[msg.view].Loading() {
			return m, m.refreshTick(msg.view)
		}
		wasLoading := m.anyLoading()
		cmd := tea.Batch(m.views[msg.view].Init(), m.refreshTick(msg.view))
		if wasLoading {
			return m, cmd
		}
		return m, tea.Batch(cmd, spinnerTick())

	case tea.KeyMsg:
		// While the help overlay is open, any key closes it.
		if m.helpOpen {
			m.helpOpen = false
			return m, nil
		}
		// While the keybind editor is open it captures all keys (including,
		// during capture, keys that are normally global).
		if m.keysEd != nil {
			change, closed := m.keysEd.Update(msg, m.cfg.Keys)
			if closed {
				m.keysEd = nil
				m.settings = newConfigOverlay() // back to the config overlay
				return m, nil
			}
			if change != nil {
				m.applyKeybind(change)
			}
			return m, nil
		}
		// While the config overlay is open it captures all keys. A committed
		// change lands in three places: the live cfg, the config file, and
		// whatever live re-apply the path warrants.
		if m.settings != nil {
			change, closed := m.settings.Update(msg, m.cfg)
			if closed {
				m.settings = nil
				return m, nil
			}
			if change != nil {
				if change.s.kind == kindAction {
					return m, m.runAction(change.s.path)
				}
				change.s.set(&m.cfg, change.val)
				if err := config.Set(change.s.path, change.fileValue()); err != nil {
					m.settings.errMsg = err.Error()
				}
				return m, m.applyConfigChange(change.s.path)
			}
			return m, nil
		}
		// While the cross-reference picker is open it captures all keys.
		if m.picker != nil {
			switch m.picker.Update(msg) {
			case ui.PickerCancel:
				m.picker, m.pickerRefs = nil, nil
			case ui.PickerConfirm:
				ref := m.pickerRefs[m.picker.Index()]
				m.picker, m.pickerRefs = nil, nil
				return m, m.followRef(ref)
			case ui.PickerOpenURL:
				// Open the selected ref in the browser, where it has a URL.
				if ref := m.pickerRefs[m.picker.Index()]; ref.URL != "" {
					m.picker, m.pickerRefs = nil, nil
					return m, ui.OpenURL(ref.URL)
				}
			}
			return m, nil
		}
		// While the filter modal is open it captures all keys.
		if m.filter != nil {
			done, cancelled := m.filter.Update(msg)
			switch {
			case cancelled:
				m.filter = nil
			case done:
				if f, ok := m.views[m.current].(filterable); ok {
					f.SetFilter(m.filter.Query(), m.filter.EnabledFields(), m.filter.CaseSensitive())
				}
				m.filter = nil
				m.syncPreviewKey(false)
			}
			return m, nil
		}
		// The config overlay opens from anywhere, even while a view captures
		// text input, but only for non-printable bindings (the ctrl+s
		// default): a user-rebound printable key must not steal characters
		// from filters and comment bodies.
		if key.Matches(msg, m.keys.Config) && !isTextKey(msg) {
			m.settings = newConfigOverlay()
			return m, nil
		}
		// While the focused view is capturing text input, route everything to
		// it (except a hard ctrl+c quit) so global bindings don't steal keys.
		if len(m.views) > 0 && m.views[m.current].InputActive() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m.updateCurrent(msg)
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.NextView):
			m.current = (m.current + 1) % len(m.views)
			m.syncPreviewKey(true)
			m.concealTransient()
			return m, nil
		case key.Matches(msg, m.keys.PrevView):
			m.current = (m.current - 1 + len(m.views)) % len(m.views)
			m.syncPreviewKey(true)
			m.concealTransient()
			return m, nil
		case key.Matches(msg, m.keys.Config):
			// Printable config bindings land here, after input routing.
			m.settings = newConfigOverlay()
			return m, nil
		case key.Matches(msg, m.keys.Help):
			m.helpOpen = true
			return m, nil
		case key.Matches(msg, m.keys.Zoom):
			m.zoomed = !m.zoomed
			m.layout() // preview width changed; views re-wrap their content
			return m, nil
		case key.Matches(msg, m.keys.TogglePreview):
			// A deliberate toggle overrides any transient reveal state.
			m.previewHidden, m.previewTransient = !m.previewHidden, false
			m.layout()
			return m, nil
		case key.Matches(msg, m.keys.Refresh):
			// Init() flips the view back into its loading state. Only start a
			// spinner loop if one isn't already running (i.e. nothing was loading).
			wasLoading := m.anyLoading()
			cmd := m.views[m.current].Init()
			if wasLoading {
				return m, cmd
			}
			return m, tea.Batch(cmd, spinnerTick())
		case key.Matches(msg, m.keys.PreviewUp):
			m.scrollPreview(-1)
			return m, nil
		case key.Matches(msg, m.keys.PreviewDown):
			m.scrollPreview(1)
			return m, nil
		case key.Matches(msg, m.keys.PreviewPgUp):
			m.scrollPreview(-(m.contentHeight() - 2))
			return m, nil
		case key.Matches(msg, m.keys.PreviewPgDn):
			m.scrollPreview(m.contentHeight() - 2)
			return m, nil
		case key.Matches(msg, m.keys.Follow):
			// Follow a cross-reference: always confirm via the picker (even for
			// a single target) so navigation never happens without a prompt.
			if refs := m.currentRefs(); len(refs) > 0 {
				items, aligned := m.pickerItems(refs)
				p := ui.NewPicker("Follow reference", items)
				m.picker, m.pickerRefs = &p, aligned
				return m, nil
			}
			// No references: fall through to the view.
		case key.Matches(msg, m.keys.Filter):
			if f, ok := m.views[m.current].(filterable); ok && !m.views[m.current].InputActive() {
				query, enabled, cs := f.FilterState()
				fm := ui.NewFilterModal("Filter "+m.views[m.current].Title(), query, f.Fields(), enabled, cs)
				m.filter = &fm
				return m, nil
			}
		default:
			// Number keys 1..9 jump straight to that view (by tab position).
			// Reached only after the modal/input-capture guards above, so a digit
			// typed into a filter still types normally.
			if i := viewIndexForKey(msg.String()); i >= 0 && i < len(m.views) {
				m.current = i
				m.syncPreviewKey(true)
				return m, nil
			}
		}
		// Anything else goes to the focused view.
		return m.updateCurrent(msg)
	}

	// Non-key messages (data-fetch results, spinner ticks) are broadcast to
	// every view; each ignores messages that aren't its own.
	return m.broadcast(msg)
}

// previewJumper is optionally implemented by views that want to scroll the
// preview pane to a specific rendered line (e.g. jumping between inline
// review threads in a diff). TakePreviewJump returns each request once.
type previewJumper interface {
	TakePreviewJump() (line int, ok bool)
}

// isTextKey reports whether the key press would insert text if routed to an
// input (a letter, digit, space; not a chord like ctrl+s).
func isTextKey(msg tea.KeyMsg) bool {
	kp, ok := tea.Msg(msg).(tea.KeyPressMsg)
	return ok && kp.Text != ""
}

// updateCurrent threads a message through only the focused view.
func (m Model) updateCurrent(msg tea.Msg) (tea.Model, tea.Cmd) {
	if len(m.views) == 0 {
		return m, nil
	}
	cmd := m.views[m.current].Update(msg)
	m.syncPreviewKey(false) // a key may have moved the selection
	if j, ok := m.views[m.current].(previewJumper); ok {
		if line, jump := j.TakePreviewJump(); jump {
			// Put the target line near the top of the viewport.
			lines := strings.Count(m.views[m.current].PreviewView(), "\n") + 1
			maxOff := max(0, lines-m.contentHeight())
			m.previewScroll = clamp(line-1, 0, maxOff)
		}
	}
	return m, cmd
}

// broadcast threads a message through every view, collecting their commands.
func (m Model) broadcast(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0, len(m.views))
	for _, v := range m.views {
		if cmd := v.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	m.syncPreviewKey(false) // a data load may have changed the selection
	return m, tea.Batch(cmds...)
}

// concealTransient ends a transient reveal (see ui.ConcealPreviewMsg).
func (m *Model) concealTransient() {
	if m.previewTransient {
		m.previewHidden, m.previewTransient = true, false
		m.layout()
	}
}

// syncPreviewKey resets the preview scroll to the top when the selected item
// changes (or always, when force is set, e.g. on a view switch).
func (m *Model) syncPreviewKey(force bool) {
	if len(m.views) == 0 {
		return
	}
	k := m.views[m.current].PreviewKey()
	if force || k != m.previewKey {
		m.previewKey = k
		m.previewScroll = 0
	}
}

// scrollPreview moves the preview offset by delta lines, clamped to content.
func (m *Model) scrollPreview(delta int) {
	lines := strings.Count(m.views[m.current].PreviewView(), "\n") + 1
	maxOff := max(0, lines-m.contentHeight())
	m.previewScroll = clamp(m.previewScroll+delta, 0, maxOff)
}

func (m Model) contentHeight() int {
	return max(1, m.height-tabBarHeight-footerHeight)
}

// applyKeybind persists one keybind edit and re-resolves whatever can apply
// live (the global chrome bindings; views capture theirs at startup).
func (m *Model) applyKeybind(change *keybindChange) {
	e := change.entry
	if m.cfg.Keys == nil {
		m.cfg.Keys = config.Keymap{}
	}
	if m.cfg.Keys[e.scope] == nil {
		m.cfg.Keys[e.scope] = map[string]config.Chord{}
	}
	m.cfg.Keys[e.scope][e.action] = config.Chord(change.keys)
	if err := config.Set("keys."+e.scope+"."+e.action, change.keys); err != nil {
		m.keysEd.errMsg = err.Error()
		return
	}
	if e.scope == "global" {
		m.keys = newKeys(m.cfg.Keys)
	}
}

// runAction executes an overlay action row.
func (m *Model) runAction(path string) tea.Cmd {
	switch path {
	case "action:edit_keybinds":
		m.settings = nil
		m.keysEd = newKeybindEditor()
		return nil
	case "action:test_notification":
		n := notify.New(m.cfg.Notify.Popup, m.cfg.Notify.Sound == nil || *m.cfg.Notify.Sound)
		if n == nil {
			m.settings.errMsg = "set popup to terminal or desktop first"
			return nil
		}
		return func() tea.Msg {
			return n.Notify("agenda test", "This is what a notification looks like.")
		}
	}
	return nil
}

// applyConfigChange re-applies whatever a just-edited config path affects
// live. Paths not handled here (notifications, view set, linear filter) only
// take effect on restart, which their overlay rows say.
func (m *Model) applyConfigChange(path string) tea.Cmd {
	switch {
	case strings.HasPrefix(path, "theme."):
		p, err := ui.ResolvePalette(m.cfg.Theme.Name, m.cfg.Theme.Palette)
		if err != nil {
			return nil
		}
		ui.SetPalette(p)
		ui.SetGlyphs(m.cfg.GlyphsEnabled())
		m.theme = defaultTheme()
	case path == "grouping":
		return groupingCmd(m.cfg.Grouping)
	case path == "hide_preview":
		m.previewHidden, m.previewTransient = m.cfg.HidePreview, false
		m.layout()
	case strings.HasPrefix(path, "refresh."):
		m.refresh = refreshIntervals(m.cfg, m.views)
		m.refreshGen++ // orphan the old tick loops
		cmds := make([]tea.Cmd, 0, len(m.views))
		for i := range m.views {
			if cmd := m.refreshTick(i); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return tea.Batch(cmds...)
	}
	return nil
}

// filterable is implemented by views that support the scoped filter popup.
type filterable interface {
	Fields() []string
	FilterState() (string, []string, bool)
	SetFilter(query string, enabled []string, caseSensitive bool)
}

// overlayProvider is optionally implemented by views that render their own
// centered modal (e.g. the PR review popup). A non-empty Overlay() is
// composited over the content; the view keeps receiving keys through the
// normal InputActive routing.
type overlayProvider interface {
	Overlay() string
}

// scroller is implemented by views whose list can be scrolled by the mouse wheel.
type scroller interface {
	ScrollList(n int)
}

// scrollList forwards a wheel scroll to the focused view's list, if it supports it.
func (m *Model) scrollList(n int) {
	if len(m.views) == 0 {
		return
	}
	if s, ok := m.views[m.current].(scroller); ok {
		s.ScrollList(n)
		m.syncPreviewKey(false) // scrolling may move the selection
	}
}

// currentRefs is the cross-references the focused view exposes for its
// selection, filtered to those we can act on — either a loaded view resolves
// them, or they carry a browser-fallback URL. This drops regex false-positives
// (no resolver, no URL) while keeping links to items that aren't loaded. nil if
// the view isn't a Referencer.
func (m Model) currentRefs() []ui.Ref {
	r, ok := m.views[m.current].(ui.Referencer)
	if !ok {
		return nil
	}
	var out []ui.Ref
	for _, ref := range r.Refs() {
		if m.resolves(ref) || ref.URL != "" {
			out = append(out, ref)
		}
	}
	return out
}

// resolves reports whether a loaded view can select the ref's target.
func (m Model) resolves(ref ui.Ref) bool {
	for _, v := range m.views {
		if t, ok := v.(ui.RefTarget); ok && t.RefKind() == ref.Kind && t.HasRef(ref.ID) {
			return true
		}
	}
	return false
}

// followRef jumps to the ref's target if a view can resolve it, otherwise opens
// its URL in the browser. Returns the command to run (nil for an in-app jump).
func (m *Model) followRef(ref ui.Ref) tea.Cmd {
	for i, v := range m.views {
		if t, ok := v.(ui.RefTarget); ok && t.RefKind() == ref.Kind && t.HasRef(ref.ID) {
			t.SelectRef(ref.ID)
			m.current = i
			m.syncPreviewKey(true)
			return nil
		}
	}
	return ui.OpenURL(ref.URL) // unresolved → browser (no-op if URL is "")
}

// pickerItems builds the picker entries from refs and a parallel ref slice
// aligned to them (a zero Ref sits at any separator row, which is never
// selectable). Browser-bound refs get a ↗; a "sessions" separator divides the
// issue/PR refs from the agent-session refs. Each ref's context snippet becomes
// the dimmed detail line.
func (m Model) pickerItems(refs []ui.Ref) ([]ui.PickerItem, []ui.Ref) {
	var items []ui.PickerItem
	var aligned []ui.Ref
	hasPrimary, sepDone := false, false
	for _, r := range refs {
		if r.Kind == "session" && hasPrimary && !sepDone {
			items = append(items, ui.PickerItem{Separator: true, Label: "sessions"})
			aligned = append(aligned, ui.Ref{})
			sepDone = true
		}
		if r.Kind != "session" {
			hasPrimary = true
		}
		label := r.Label
		if !m.resolves(r) {
			label += "  ↗"
		}
		items = append(items, ui.PickerItem{Label: label, Detail: r.Detail})
		aligned = append(aligned, r)
	}
	return items, aligned
}

// dims computes the pane sizes. previewContentW leaves room for the preview's
// border + padding (3) and its scrollbar gutter (2). When zoomed the preview
// takes the whole width and the list drops out.
func (m Model) dims() (listW, previewContentW, contentH int) {
	contentH = max(1, m.height-tabBarHeight-footerHeight)
	previewPane := m.width * previewRatio / 100
	if m.zoomed {
		previewPane = m.width
	} else if m.previewHidden {
		previewPane = 0 // nav-only: the list takes the full width
	}
	listW = m.width - previewPane
	previewContentW = max(1, previewPane-3-scrollGutter)
	return
}

// scrollGutter is the width reserved for a scrollbar (the bar + a gap).
const scrollGutter = 2

// layout recomputes per-view sizes after a resize.
func (m *Model) layout() {
	if !m.ready {
		return
	}
	listW, previewContentW, contentH := m.dims()
	for _, v := range m.views {
		v.SetSize(listW, previewContentW, contentH)
	}
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // enable mouse wheel events
	if !m.ready || len(m.views) == 0 {
		v.Content = "Loading agenda…"
		return v
	}

	_, previewContentW, contentH := m.dims()
	cur := m.views[m.current]

	// Clip each pane to the content height so tall content can't overflow and
	// push the footer off-screen. The list manages its own window; the preview
	// is clipped from the scroll offset and gets a scrollbar when it overflows.
	// Zoomed, the preview stands alone at full width (no list, no border).
	var body string
	if m.zoomed {
		body = m.theme.previewZoomed.Height(contentH).Render(m.previewPane(cur, previewContentW, contentH))
	} else if m.previewHidden {
		body = clipFrom(cur.ListView(), 0, contentH)
	} else {
		body = lipgloss.JoinHorizontal(
			lipgloss.Top,
			clipFrom(cur.ListView(), 0, contentH),
			m.theme.preview.Height(contentH).Render(m.previewPane(cur, previewContentW, contentH)),
		)
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderTabs(),
		body,
		m.renderFooter(),
	)

	// Composite the picker modal centered over the content, if open.
	if m.picker != nil {
		box := m.picker.View()
		x := max(0, (m.width-lipgloss.Width(box))/2)
		y := max(0, (m.height-lipgloss.Height(box))/2)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}

	// Composite the filter modal centered over the content, if open.
	if m.filter != nil {
		box := m.filter.View()
		x := max(0, (m.width-lipgloss.Width(box))/2)
		y := max(0, (m.height-lipgloss.Height(box))/2)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}

	// Composite the focused view's own modal (e.g. the PR review popup),
	// centered, when it has one.
	if o, ok := cur.(overlayProvider); ok {
		if box := o.Overlay(); box != "" {
			x := max(0, (m.width-lipgloss.Width(box))/2)
			y := max(0, (m.height-lipgloss.Height(box))/2)
			content = lipgloss.NewCompositor(
				lipgloss.NewLayer(content),
				lipgloss.NewLayer(box).X(x).Y(y).Z(1),
			).Render()
		}
	}

	// Composite the help overlay centered over the content, if open.
	if m.helpOpen {
		box := m.helpView()
		x := max(0, (m.width-lipgloss.Width(box))/2)
		y := max(0, (m.height-lipgloss.Height(box))/2)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}

	// Composite the keybind editor centered over the content, if open.
	if m.keysEd != nil {
		box := m.keysEd.View(m.cfg.Keys, m.contentHeight()-10)
		x := max(0, (m.width-lipgloss.Width(box))/2)
		y := max(0, (m.height-lipgloss.Height(box))/2)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}

	// Composite the config overlay centered over the content, if open.
	if m.settings != nil {
		box := m.settings.View(m.cfg)
		x := max(0, (m.width-lipgloss.Width(box))/2)
		y := max(0, (m.height-lipgloss.Height(box))/2)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(y).Z(1),
		).Render()
	}

	// The notification toast sits top-right, above everything.
	if m.toast != nil {
		box := m.renderToast()
		x := max(0, m.width-lipgloss.Width(box)-2)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(tabBarHeight).Z(2),
		).Render()
	}

	v.Content = content
	return v
}

// renderToast draws the in-app notification popup.
func (m Model) renderToast() string {
	t := m.toast
	title := ui.Glyph(ui.IconBell, "") + t.Title
	body := t.Body
	if lipgloss.Width(body) > 60 {
		body = ui.Truncate(body, 60)
	}
	content := ui.Yellow.Bold(true).Render(title)
	if body != "" {
		content += "\n" + body
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Yellow)).
		Padding(0, 1).
		Render(content)
}

// previewPane renders the preview content clipped to height lines from the
// scroll offset, with a scrollbar gutter on the right (a bar only when the full
// content overflows the viewport).
func (m Model) previewPane(cur View, contentW, height int) string {
	full := cur.PreviewView()
	total := strings.Count(full, "\n") + 1
	lines := strings.Split(clipFrom(full, m.previewScroll, height), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:height]
	bar := ui.Scrollbar(height, total, height, m.previewScroll)
	for i := range lines {
		pad := max(0, contentW-lipgloss.Width(lines[i]))
		lines[i] += strings.Repeat(" ", pad) + " " + bar[i]
	}
	return strings.Join(lines, "\n")
}

// clipFrom returns at most n lines of s starting at line offset, so a pane
// can't overflow its row budget. offset enables scrolling.
func clipFrom(s string, offset, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	offset = clamp(offset, 0, len(lines))
	lines = lines[offset:]
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// viewIndexForKey maps a single-digit key string ("1".."9") to a 0-based view
// index, or -1 if the key isn't a 1..9 digit. "1" → view 0, matching the tab
// labels.
func viewIndexForKey(s string) int {
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		return int(s[0] - '1')
	}
	return -1
}

func (m Model) renderTabs() string {
	labels := make([]string, len(m.views))
	for i, v := range m.views {
		style := m.theme.tabInactive
		if i == m.current {
			style = m.theme.tabActive
		}
		// Prefix the 1-based index as a jump hint (matches the 1..9 hotkeys),
		// and the view's icon when decorative glyphs are on.
		label := tabIcon(v.Title()) + v.Title()
		if i < 9 {
			label = string(rune('1'+i)) + " " + label
		}
		// Append a spinner glyph while the view is fetching.
		if v.Loading() {
			label += " " + spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		}
		labels[i] = style.Render(label)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Bottom, labels...)
	if tag := m.versionTag(); tag != "" {
		gap := m.width - lipgloss.Width(row) - lipgloss.Width(tag) - 2
		if gap > 0 {
			row += strings.Repeat(" ", gap) + tag
		}
	}
	return m.theme.tabBar.Width(m.width).Render(row)
}

// versionTag is the right-hand tab-bar label: the running version, plus an
// accent-coloured arrow when a newer release exists.
func (m Model) versionTag() string {
	if m.version == "" {
		return ""
	}
	tag := ui.Dim.Render("v" + strings.TrimPrefix(m.version, "v"))
	if m.newer != "" {
		// Yellow-bold: the palette's attention colour everywhere else (pending
		// checks, review required), and unlike Accent it is not already the
		// colour of the active tab beside it.
		tag += "  " + ui.Yellow.Bold(true).Render("↑v"+strings.TrimPrefix(m.newer, "v"))
	}
	return tag
}

// glyphLegend explains the focused view's row glyphs; a grey dot always
// means "nothing to show" for that slot.
func glyphLegend(title string) []string {
	switch title {
	case "PRs":
		return []string{
			ui.Dim.Render("state  ") + ui.Green.Render(ui.IconOpen) + " open  " +
				ui.Magenta.Render(ui.IconMerged) + " merged  " +
				ui.Red.Render(ui.IconClosed) + " closed  " +
				ui.Dim.Render(ui.IconDraft) + " draft",
			ui.Dim.Render("checks ") + ui.Green.Render(ui.IconCIOK) + " passing  " +
				ui.Red.Render(ui.IconCIFail) + " failing  " +
				ui.Yellow.Render(ui.IconCIPending) + " running  " +
				ui.Dim.Render(ui.IconDot) + " none",
			ui.Dim.Render("review ") + ui.Green.Render(ui.IconApproved) + " approved  " +
				ui.Red.Render(ui.IconChanges) + " changes  " +
				ui.Yellow.Render(ui.IconReviewReq) + " required  " +
				ui.Dim.Render(ui.IconDot) + " none",
		}
	case "Linear":
		return []string{
			ui.Dim.Render("priority ") + ui.Red.Bold(true).Render("!") + " urgent  " +
				ui.Yellow.Render("↑") + " high  " +
				ui.Blue.Render("•") + " medium  " +
				ui.Dim.Render("↓") + " low  " +
				ui.Dim.Render("·") + " none",
			ui.Dim.Render("inbox    ") + ui.Accent.Render("●") + " unread  " +
				ui.Dim.Render("○") + " read",
		}
	case "Sessions":
		return []string{
			ui.Dim.Render("⚙ before the agent icon marks a programmatic (SDK) session"),
		}
	}
	return nil
}

// tabIcon is the decorative Nerd Font icon for a view's tab label.
func tabIcon(title string) string {
	switch title {
	case "PRs":
		return ui.Glyph(ui.IconTabPRs, "")
	case "Sessions":
		return ui.Glyph(ui.IconTabSessions, "")
	case "Linear":
		return ui.Glyph(ui.IconTabLinear, "")
	}
	return ""
}

// helpView renders the '?' overlay: every binding for the focused view, then
// the global chrome keys, then the list-navigation keys (which live inside
// the list widget and have no key.Binding help of their own).
func (m Model) helpView() string {
	var b strings.Builder
	line := func(k, desc string) {
		fmt.Fprintf(&b, "  %s %s\n", ui.Accent.Bold(true).Render(fmt.Sprintf("%-10s", k)), desc)
	}
	section := func(title string) {
		b.WriteString(ui.Dim.Render(title))
		b.WriteByte('\n')
	}

	b.WriteString(ui.Bold.Render("Keys"))
	b.WriteString("\n\n")
	section(m.views[m.current].Title())
	for _, bnd := range m.views[m.current].Bindings() {
		if h := bnd.Help(); h.Key != "" {
			line(h.Key, h.Desc)
		}
	}
	b.WriteByte('\n')
	section("Global")
	for _, bnd := range []key.Binding{
		m.keys.Follow, m.keys.Filter, m.keys.Zoom, m.keys.TogglePreview,
		m.keys.NextView, m.keys.PrevView, m.keys.PreviewUp, m.keys.Refresh,
		m.keys.Config, m.keys.Quit,
	} {
		if h := bnd.Help(); h.Key != "" {
			line(h.Key, h.Desc)
		}
	}
	line("1..9", "jump to view")
	b.WriteByte('\n')
	section("List")
	line("j/k ↑/↓", "move")
	line("g/G", "top / bottom")
	line("ctrl+u/d", "half page")
	line("/", "quick filter")
	line("esc", "clear filter")

	if legend := glyphLegend(m.views[m.current].Title()); len(legend) > 0 {
		b.WriteByte('\n')
		section("Glyphs")
		for _, l := range legend {
			b.WriteString("  " + l + "\n")
		}
	}

	b.WriteByte('\n')
	b.WriteString(ui.Faint.Render("any key closes"))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Render(b.String())
}

// footerLine renders one candidate footer hint row.
func (m Model) footerLine(bindings []key.Binding) string {
	var b strings.Builder
	first := true
	for _, bnd := range bindings {
		h := bnd.Help()
		if h.Key == "" {
			continue
		}
		if !first {
			b.WriteString(m.theme.footerSep.String())
		}
		first = false
		b.WriteString(m.theme.footerKey.Render(h.Key))
		b.WriteString(" ")
		b.WriteString(m.theme.footerDesc.Render(h.Desc))
	}
	return b.String()
}

func (m Model) renderFooter() string {
	// Prefer the full hint row (every view binding plus the global keys, as
	// the footer always showed). When it no longer fits next to the status,
	// fall back to a compact row and let '?' carry the rest.
	view := m.views[m.current].Bindings()
	var follow []key.Binding
	if len(m.currentRefs()) > 0 {
		follow = append(follow, m.keys.Follow)
	}

	// Zooming an already-hidden preview makes no sense, so the zoom hint
	// only shows while the preview is in view (v brings it back first).
	pane := []key.Binding{m.keys.TogglePreview}
	if !m.previewHidden {
		pane = append(pane, m.keys.Zoom)
	}
	full := append(append(append(append([]key.Binding{}, view...), follow...),
		m.keys.Filter), pane...)
	full = append(full, m.keys.NextView, m.keys.PreviewUp, m.keys.Refresh,
		m.keys.Config, m.keys.Help, m.keys.Quit)

	status := m.views[m.current].Status()
	left := m.footerLine(full)
	if lipgloss.Width(left)+lipgloss.Width(status)+1 > m.width {
		compact := view
		if len(compact) > 4 {
			compact = compact[:4]
		}
		compact = append(append(append(append([]key.Binding{}, compact...), follow...),
			m.keys.Filter), pane...)
		compact = append(compact, m.keys.Help, m.keys.Quit)
		left = m.footerLine(compact)
	}

	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(status))
	return m.theme.footer.Width(m.width).Render(
		left + strings.Repeat(" ", gap) + status,
	)
}
