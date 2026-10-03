package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	// The floating detail's share of the screen (percent), when the preview
	// pane is off and 'v' reveals one row's detail.
	floatWRatio = 70
	floatHRatio = 70
	// The float's border (2) and horizontal padding (2), which Width()
	// counts as part of the box.
	floatChrome = 4
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
	// previewPeeked marks a pane hidden by the toggle while hide_preview is
	// off: the pane is the configured state, so moving to another row
	// brings it back rather than leaving it hidden indefinitely.
	previewPeeked bool

	// preview scrolling, owned centrally so it works the same in every view.
	previewScroll int
	previewKey    string
	// previewCache holds the last rendered preview and its line count.
	// Scrolling and drawing both need them, and re-rendering a long markdown
	// body per event is what made a fast scroll stall. It sits behind a
	// pointer so the value-receiver View can fill it.
	previewCache *previewCache
	// frame holds the last composed view, reused when a message changed
	// nothing on screen (a wheel event against the end of the preview).
	frame *frameCache

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

	// status is the app-level message log (fetch failures, stale data,
	// completed actions). The newest renders in the status row; errors open
	// statusOpen on arrival so their detail is not buried behind a key.
	status     []ui.StatusMsg
	statusOpen bool

	// lastClick remembers the previous row click, so a second click on the
	// same row soon after counts as a double-click.
	lastClick clickRecord

	// wheelSt tells a wheel notch's burst of events from separate scrolls.
	wheelSt *wheelState

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
		wheelSt:       &wheelState{},
		previewCache:  &previewCache{},
		frame:         &frameCache{},
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
	// Config keys that match nothing are reported rather than ignored: a typo
	// otherwise looks exactly like the feature not working.
	if len(m.cfg.Unknown) > 0 {
		keys := strings.Join(m.cfg.Unknown, ", ")
		cmds = append(cmds, func() tea.Msg {
			return ui.Status(ui.SeverityWarn, "config",
				fmt.Sprintf("%d unknown config key(s): %s", len(m.cfg.Unknown), keys),
				"These keys parsed but match no option, so they do nothing. Check them against config.example.yml.")
		})
	}

	// Views start flat; when grouping is configured on, a broadcast flips
	// them before the first data lands.
	if m.cfg.Grouping {
		cmds = append(cmds, groupingCmd(true))
	}
	if m.cfg.TogglesPersist() {
		cmds = append(cmds, func() tea.Msg { return ui.TogglesPersistMsg(true) })
	}
	if m.cfg.UnreadEnabled() {
		cmds = append(cmds, func() tea.Msg { return ui.UnreadMsg(true) })
	}
	if m.previewHidden {
		cmds = append(cmds, func() tea.Msg { return ui.PreviewShownMsg(false) })
	}
	if m.cfg.UnreadSync {
		cmds = append(cmds, func() tea.Msg { return ui.UnreadSyncMsg(true) })
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
	// Assume the screen changed; the wheel path opts out when it provably
	// did not, so a missed case costs a redraw rather than a stale frame.
	m.invalidateFrame()
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
		return m.wheel(msg)

	case tea.MouseClickMsg:
		if !m.ready || msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.click(msg.X, msg.Y)

	case ui.StatusMsg:
		m.status = append(m.status, msg)
		if len(m.status) > statusLogMax {
			m.status = m.status[len(m.status)-statusLogMax:]
		}
		// An error is worth interrupting for: its detail says how to fix it,
		// and a one-line summary cannot carry a wrapped API message.
		if msg.Severity == ui.SeverityError && msg.Detail != "" {
			m.statusOpen = true
		}
		return m, nil
	case updateAvailableMsg:
		m.newer = string(msg)
		return m, nil
	case ui.RevealPreviewMsg:
		if m.previewHidden {
			return m, m.setPreview(false, true)
		}
		return m, nil
	case ui.ConcealPreviewMsg:
		// Sent when the selection moves on. It ends a transient reveal, and
		// equally a pane peeked away with the toggle: the pane is the
		// configured state, so the next row gets it back.
		switch {
		case m.previewTransient:
			return m, m.setPreview(true, false)
		case m.previewPeeked:
			m.previewPeeked = false
			return m, m.setPreview(false, false)
		}
		return m, nil
	case ui.ConfigSetMsg:
		// A view applied this to its own config already; persist it so it
		// survives the next run, and mirror it into the live config.
		if err := config.Set(msg.Path, msg.Value); err != nil {
			return m, func() tea.Msg {
				return ui.Status(ui.SeverityError, "config", err.Error(), "")
			}
		}
		switch msg.Path {
		case "github.filter":
			m.cfg.GitHub.Filter = msg.Value
		case "github.review_filter":
			m.cfg.GitHub.ReviewFilter = msg.Value
		}
		m.invalidateFrame()
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
		if m.statusOpen {
			m.statusOpen = false
			return m, nil
		}
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
				if err := config.Set(change.s.path, change.fileValue(m.cfg)); err != nil {
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
		case msg.String() == "esc":
			// One rule for esc: step back one layer, and never close the
			// app. The focused view gets it first, so a pane can unwind its
			// own state (a log back to its jobs, focus back to the list)
			// before the root model closes anything.
			if len(m.views) > 0 {
				if d, ok := m.views[m.current].(dismisser); ok && d.Dismiss() {
					m.invalidateFrame()
					return m, nil
				}
			}
			return m.dismiss()
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
		case key.Matches(msg, m.keys.Messages):
			m.statusOpen = !m.statusOpen
			return m, nil
		case key.Matches(msg, m.keys.TogglePreview):
			// With the pane configured off, 'v' shows the detail as a float
			// over the list rather than splitting it, and pressing it again
			// closes that. Otherwise it is the plain pane toggle.
			if m.cfg.HidePreview {
				open := !m.floating()
				cmd := m.setPreview(!open, open)
				// A float takes the keys however it was opened, so 'v' is
				// the same as 'c', 'd' or 't' in that respect: the list is
				// behind it either way.
				if f, ok := m.views[m.current].(paneFocuser); ok {
					f.FocusPane(open)
				}
				return m, cmd
			}
			// The pane is the configured state, so hiding it is a peek at
			// this row's list entry: the next row brings the pane back.
			m.previewPeeked = !m.previewHidden
			return m, m.setPreview(!m.previewHidden, false)
		case key.Matches(msg, m.keys.Refresh):
			// Init() flips the view back into its loading state. Only start a
			// spinner loop if one isn't already running (i.e. nothing was loading).
			wasLoading := m.anyLoading()
			cmd := m.views[m.current].Init()
			if wasLoading {
				return m, cmd
			}
			return m, tea.Batch(cmd, spinnerTick())
		case m.paneFocused() && (msg.String() == "up" || msg.String() == "down"):
			// A focused scrolling pane takes the plain arrows: the list is
			// dimmed, so moving it would be invisible anyway.
			d := 1
			if msg.String() == "up" {
				d = -1
			}
			m.scrollPreview(d)
			return m, nil
		case m.paneFocused() && (msg.String() == "pgup" || msg.String() == "pgdown"):
			d := m.contentHeight() - 2
			if msg.String() == "pgup" {
				d = -d
			}
			m.scrollPreview(d)
			return m, nil
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

// previewFocuser is optionally implemented by views whose right pane can
// take the keys (the PR view's jobs pane). PreviewFocus names the pane while
// it has them, "" otherwise: the list dims, the pane's border lights up, and
// the footer says where the keys went.
type previewFocuser interface {
	PreviewFocus() string
}

// previewFocus is the name of the pane holding the keys, "" when the list
// has them.
func (m Model) previewFocus() string {
	if len(m.views) == 0 {
		return ""
	}
	if f, ok := m.views[m.current].(previewFocuser); ok {
		return f.PreviewFocus()
	}
	return ""
}

// focusKeeper is optionally implemented by a previewFocuser that wants part
// of its list left lit while the preview has the keys: the row the pane is
// about. first and n are lines of the ListView output.
type focusKeeper interface {
	FocusKeepLines() (first, n int, ok bool)
}

// dimList greys the list out while the preview has the keys, stripped of its
// colours so nothing in it reads as live, not merely darker. Lines from keep
// for n are left alone: the row the preview is showing stays as it was.
func dimList(s string, keep, n int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if i >= keep && i < keep+n {
			continue
		}
		lines[i] = ui.Dim.Render(ansi.Strip(l))
	}
	return strings.Join(lines, "\n")
}

// previewScroller is optionally implemented by views that scroll the preview
// by a relative amount (e.g. j/k through a log shown there). Relative, so the
// offset stays the model's: the wheel may have moved it since the view last
// looked. TakePreviewScroll returns each request once.
type previewScroller interface {
	TakePreviewScroll() (delta int, ok bool)
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
	m.applyPreviewRequests()
	return m, cmd
}

// applyPreviewRequests carries out a scroll the focused view asked for: a
// jump to a line, or a relative scroll.
func (m *Model) applyPreviewRequests() {
	cur := m.views[m.current]
	if j, ok := cur.(previewJumper); ok {
		if line, jump := j.TakePreviewJump(); jump {
			// Put the target line near the top of the viewport.
			_, lines := m.renderedPreview(cur)
			maxOff := max(0, lines-m.contentHeight())
			m.previewScroll = clamp(line-1, 0, maxOff)
		}
	}
	if s, ok := cur.(previewScroller); ok {
		if d, scroll := s.TakePreviewScroll(); scroll {
			m.scrollPreview(d)
		}
	}
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
	// A load can ask for a scroll too: a log that arrives positions itself
	// at its first error.
	if len(m.views) > 0 {
		m.applyPreviewRequests()
	}
	return m, tea.Batch(cmds...)
}

// commitSetting writes a change to the live config, the file, and whatever
// live re-apply its path warrants. Shared by the keyboard and the mouse so
// the two cannot drift.
func (m *Model) commitSetting(change *settingChange) tea.Cmd {
	if change.s.kind == kindAction {
		return m.runAction(change.s.path)
	}
	change.s.set(&m.cfg, change.val)
	if err := config.Set(change.s.path, change.fileValue(m.cfg)); err != nil {
		m.settings.errMsg = err.Error()
	}
	return m.applyConfigChange(change.s.path)
}

// concealTransient ends a transient reveal (see ui.ConcealPreviewMsg), and
// its mirror: a pane peeked away with the toggle comes back, since the
// pane is what the config asks for.
func (m *Model) concealTransient() {
	switch {
	case m.previewTransient:
		_ = m.setPreview(true, false)
	case m.previewPeeked:
		m.previewPeeked = false
		_ = m.setPreview(false, false)
	}
}

// setPreview moves the preview between hidden and shown. One place owns the
// flags so every path (the key, a reveal, a config change) agrees on what
// "transient" means, which is what the float keys off. It also tells the
// views which state they are in: what counts as reading a row depends on
// whether the detail is actually on screen.
func (m *Model) setPreview(hidden, transient bool) tea.Cmd {
	was, wasFloat := m.previewHidden, m.floating()
	m.previewHidden, m.previewTransient = hidden, transient
	m.layout()
	var cmds []tea.Cmd
	if was != hidden {
		shown := !hidden
		cmds = append(cmds, func() tea.Msg { return ui.PreviewShownMsg(shown) })
	}
	// Batch runs these concurrently, so views cannot assume an order: each
	// handler settles focus from the combined state.
	if isFloat := m.floating(); wasFloat != isFloat {
		cmds = append(cmds, func() tea.Msg { return ui.PreviewFloatingMsg(isFloat) })
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// floating reports whether the detail should render as a centered overlay
// rather than a side pane: the pane is off, and something revealed it for
// this row only. Zoom still wins, being an explicit full-screen request.
func (m Model) floating() bool {
	return m.previewTransient && !m.zoomed
}

// floatBox is the floating detail's screen rectangle: where it is drawn
// and how big. One helper so the render and the click routing cannot
// disagree about where the box is; contentX/contentY are the first cell
// inside the border and padding, which is where the body starts.
func (m Model) floatBox() (x, y, w, h, contentX, contentY int) {
	_, previewContentW, _ := m.dims()
	_, fh := m.floatDims()
	w = previewContentW + scrollGutter + floatChrome
	h = fh + 2 // the box's own top and bottom border
	x = max(0, (m.width-w)/2)
	// The box is centered on the whole screen, matching overlayCentered.
	y = max(0, (m.height-h)/2)
	return x, y, w, h, x + 2, y + 1 // border + 1 padding column, border row
}

// floatDims sizes the floating detail box: a readable column that still
// leaves the list visible around it. The height is bounded by the content
// region, not the screen, so the box can never push the footer off.
func (m Model) floatDims() (w, h int) {
	contentH := max(1, m.height-tabBarHeight-footerHeight-m.statusHeight())
	w = max(20, min(m.width*floatWRatio/100, m.width-4))
	h = max(1, min(m.height*floatHRatio/100, contentH-2))
	return w, h
}

// syncPreviewKey resets the preview scroll to the top when the selected item
// changes (or always, when force is set, e.g. on a view switch).
func (m *Model) syncPreviewKey(force bool) {
	// Every path that can move the selection calls this, so the cache drop
	// belongs here rather than at each call site.
	m.previewInvalidate()
	if len(m.views) == 0 {
		return
	}
	k := m.views[m.current].PreviewKey()
	if force || k != m.previewKey {
		m.previewKey = k
		m.previewScroll = 0
	}
}

// frameCache holds the last composed frame so a no-op message can reuse it
// instead of re-rendering the whole screen.
type frameCache struct {
	content string
	valid   bool
}

// frameDirty reports whether the next View must compose a new frame.
func (m Model) frameDirty() bool { return m.frame == nil || !m.frame.valid }

// keepFrame re-validates the composed frame after Update invalidated it,
// for a message that provably changed nothing on screen.
func (m *Model) keepFrame() {
	if m.frame != nil && m.frame.content != "" {
		m.frame.valid = true
	}
}

// invalidateFrame marks the composed frame stale. Everything except a no-op
// scroll goes through here, so reuse is opt-in and a missed call costs a
// redraw rather than a stale screen.
func (m *Model) invalidateFrame() {
	if m.frame != nil {
		m.frame.valid = false
	}
}

// previewCache memoizes one rendered preview and its line count, so a burst
// of scroll events measures and re-renders nothing.
type previewCache struct {
	key   string
	text  string
	split []string // text pre-split, so a scroll slices instead of re-splitting
}

// previewInvalidate drops the cached preview, so the next render re-asks the
// view. Anything that can change the preview calls this; only the scroll and
// draw paths rely on the cache surviving.
func (m *Model) previewInvalidate() {
	if m.previewCache != nil {
		m.previewCache.key = ""
	}
}

// previewCacheKey identifies the current preview render: which view, which
// item, and the size it was laid out at.
func (m Model) previewCacheKey() string {
	_, prevW, contentH := m.dims()
	return fmt.Sprintf("%d:%s:%d:%d", m.current, m.previewKey, prevW, contentH)
}

// renderedPreview returns the preview text and its line count, rendering only
// when something that affects it has changed.
func (m Model) renderedPreview(cur View) (string, int) {
	split := m.previewSplit(cur)
	return m.previewCache.text, len(split)
}

// previewSplit returns the rendered preview as lines, rendering and splitting
// only when something that affects the preview has changed.
func (m Model) previewSplit(cur View) []string {
	if m.previewCache == nil {
		return strings.Split(cur.PreviewView(), "\n")
	}
	if key := m.previewCacheKey(); key != m.previewCache.key {
		text := cur.PreviewView()
		m.previewCache.key = key
		m.previewCache.text = text
		m.previewCache.split = strings.Split(text, "\n")
	}
	return m.previewCache.split
}

// scrollPreview moves the preview offset by delta lines, clamped to content.
// It reports whether the offset actually moved: scrolling past either end
// changes nothing, and redrawing for that is what made a wheel spun at the
// boundary feel like it had a queue to work through.
func (m *Model) scrollPreview(delta int) bool {
	_, lines := m.renderedPreview(m.views[m.current])
	maxOff := max(0, lines-m.contentHeight())
	next := clamp(m.previewScroll+delta, 0, maxOff)
	if next == m.previewScroll {
		return false
	}
	m.previewScroll = next
	return true
}

func (m Model) contentHeight() int {
	return max(1, m.height-tabBarHeight-footerHeight-m.statusHeight())
}

// statusHeight is the row the status line occupies, if it has anything to say.
func (m Model) statusHeight() int {
	if m.statusLine() == "" {
		return 0
	}
	return 1
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
	case "action:reset_filters":
		// Back to what a fresh install would use, both searches, written
		// through so the file matches what the list is doing.
		def := config.Default().GitHub
		for path, val := range map[string]string{
			"github.filter":        def.Filter,
			"github.review_filter": def.ReviewFilter,
		} {
			if err := config.Set(path, val); err != nil {
				m.settings.errMsg = err.Error()
				return nil
			}
		}
		m.cfg.GitHub.Filter, m.cfg.GitHub.ReviewFilter = def.Filter, def.ReviewFilter
		m.settings.errMsg = ""
		return nil
	case "action:test_notification":
		n := notify.New(m.cfg.Notify.Popup, m.cfg.Notify.Sound == nil || *m.cfg.Notify.Sound, m.cfg.Notify.ClickAction())
		if n == nil {
			m.settings.errMsg = "set popup to terminal or desktop first"
			return nil
		}
		return func() tea.Msg {
			return n.Notify("agenda test", "This is what a notification looks like.", "https://github.com/sanity-labs/agenda")
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
	case path == "toggles":
		persist := m.cfg.TogglesPersist()
		return func() tea.Msg { return ui.TogglesPersistMsg(persist) }
	case path == "unread":
		on := m.cfg.UnreadEnabled()
		return func() tea.Msg { return ui.UnreadMsg(on) }
	case path == "unread_sync":
		on := m.cfg.UnreadSync
		return func() tea.Msg { return ui.UnreadSyncMsg(on) }
	case path == "footer":
		m.invalidateFrame()
		m.layout()
		return nil
	case path == "hide_preview":
		m.previewPeeked = false
		return m.setPreview(m.cfg.HidePreview, false)
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

// paneFocuser is optionally implemented by views whose preview pane can
// take the keys. Focus is the view's own state, since only it knows what
// its pane holds; the root model asks so it can route the arrows and the
// footer can say where they go.
type paneFocuser interface {
	// PaneFocused reports whether the pane has the keys.
	PaneFocused() bool
	// FocusPane gives the pane the keys, or takes them back. Reports
	// whether anything changed, so a no-op key does not redraw.
	FocusPane(bool) bool
	// PaneScrolls reports a pane that scrolls rather than holding its own
	// rows, so the arrows should move the preview instead of a cursor.
	PaneScrolls() bool
}

// paneFocused reports whether the active view's pane has the keys and
// scrolls, which is when the arrows belong to the preview.
func (m Model) paneFocused() bool {
	if len(m.views) == 0 {
		return false
	}
	f, ok := m.views[m.current].(paneFocuser)
	return ok && f.PaneFocused() && f.PaneScrolls()
}

// dismisser is optionally implemented by views with their own closable
// panes (a diff, comments, a jobs list). Reports whether it closed
// something, so esc can fall through to the preview when it did not.
type dismisser interface {
	Dismiss() bool
}

// dismiss closes the innermost open thing: a view's own pane first, then a
// floated detail. Never the app, which is 'q' alone.
func (m Model) dismiss() (tea.Model, tea.Cmd) {
	if m.zoomed {
		m.zoomed = false
		m.layout()
		return m, nil
	}
	if m.floating() {
		return m, m.setPreview(true, false)
	}
	return m, nil
}

// overlayProvider is optionally implemented by views that render their own
// centered modal (e.g. the PR review popup). A non-empty Overlay() is
// composited over the content; the view keeps receiving keys through the
// normal InputActive routing.
type overlayProvider interface {
	Overlay() string
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
	contentH = max(1, m.height-tabBarHeight-footerHeight-m.statusHeight())
	previewPane := m.width * previewRatio / 100
	if m.zoomed {
		previewPane = m.width
	} else if m.floating() {
		// The list keeps the full width; the detail floats over it, so it
		// is sized from the float box rather than from a pane.
		fw, _ := m.floatDims()
		return m.width, max(1, fw-floatChrome-scrollGutter), contentH
	} else if m.previewHidden {
		previewPane = 0 // nav-only: the list takes the full width
	}
	listW = m.width - previewPane
	previewContentW = max(1, previewPane-3-scrollGutter)
	return
}

// scrollGutter is the width reserved for a scrollbar (the bar + a gap).
const scrollGutter = 2

// statusLogMax caps the message log; it is a tail, not an archive.
const statusLogMax = 50

// statusLine renders the newest message, or nothing when the log is empty or
// the newest has been read (any message is cleared by opening the log).
// statusLine is gone: the toast announces a message when it arrives, and
// the footer says one is waiting, so a permanent row repeating it was the
// same warning three times over. Kept as a stub returning "" so the height
// arithmetic has one place to change if it ever comes back.
func (m Model) statusLine() string { return "" }

// unreadIssues counts messages worth pointing at: warnings and errors. A
// success notice needs no footer marker.
func (m Model) unreadIssues() int {
	n := 0
	for _, s := range m.status {
		if s.Severity == ui.SeverityWarn || s.Severity == ui.SeverityError {
			n++
		}
	}
	return n
}

// issuesHint is the footer's pointer to the message log, or "" when there
// is nothing to read.
func (m Model) issuesHint() string {
	n := m.unreadIssues()
	if n == 0 {
		return ""
	}
	keys := m.keys.Messages.Keys()
	if len(keys) == 0 {
		return ""
	}
	label := "errors"
	sev := ui.Red
	if !m.hasError() {
		sev = ui.Yellow
	}
	// The glyph carries its own trailing space; adding another leaves a gap
	// that reads as an empty field between it and the key.
	return sev.Render(ui.Glyph(ui.IconIssue, "!")) + " " +
		m.theme.footerKey.Render(keys[0]) + " " +
		m.theme.footerDesc.Render(label)
}

// hasError reports whether any message is an error rather than a warning.
func (m Model) hasError() bool {
	for _, s := range m.status {
		if s.Severity == ui.SeverityError {
			return true
		}
	}
	return false
}

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
	if m.wheelSt != nil {
		defer func() { m.wheelSt.drawnAt = time.Now() }()
	}
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // wheel and click events (see mouse.go)
	v.WindowTitle = m.windowTitle()
	if !m.ready || len(m.views) == 0 {
		v.Content = "Loading agenda…"
		return v
	}
	if m.frame != nil && m.frame.valid {
		v.Content = m.frame.content
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
	} else if m.previewHidden || m.floating() {
		body = clipFrom(cur.ListView(), 0, contentH)
	} else {
		list, pane := clipFrom(cur.ListView(), 0, contentH), m.theme.preview
		if m.previewFocus() != "" {
			keep, n := 0, 0
			if k, ok := cur.(focusKeeper); ok {
				if first, lines, shown := k.FocusKeepLines(); shown {
					keep, n = first, lines
				}
			}
			list, pane = dimList(list, keep, n), m.theme.previewActive
		}
		body = lipgloss.JoinHorizontal(
			lipgloss.Top,
			list,
			pane.Height(contentH).Render(m.previewPane(cur, previewContentW, contentH)),
		)
	}

	rows := []string{m.renderTabs(), body}
	if line := m.statusLine(); line != "" {
		rows = append(rows, line)
	}
	rows = append(rows, m.renderFooter())
	content := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// The floating detail sits over the list, under any modal.
	if m.floating() {
		_, fh := m.floatDims()
		// Width() is the box's outer width, so it has to cover the line
		// (contentW plus the scrollbar previewPane appends) as well as the
		// border and padding around it. Too narrow and every line wraps,
		// doubling the float's height until it runs off the screen.
		body := clipFrom(m.previewPane(cur, previewContentW, fh), 0, fh)
		outer := previewContentW + scrollGutter + floatChrome
		content = m.overlayCentered(content,
			m.theme.previewFloat.Width(outer).Render(body))
	}

	// Composite the picker modal centered over the content, if open.
	if m.picker != nil {
		content = m.overlayCentered(content, m.picker.View())
	}

	// Composite the filter modal centered over the content, if open.
	if m.filter != nil {
		content = m.overlayCentered(content, m.filter.View())
	}

	// Composite the focused view's own modal (e.g. the PR review popup),
	// centered, when it has one.
	if o, ok := cur.(overlayProvider); ok {
		if box := o.Overlay(); box != "" {
			content = m.overlayCentered(content, box)
		}
	}

	// Composite the message log, if open. Bordered like the other overlays:
	// without a frame it reads as part of the pane behind it.
	if m.statusOpen {
		content = m.overlayCentered(content, ui.DetailView(m.status, min(70, m.width-10)))
	}

	// Composite the help overlay centered over the content, if open.
	if m.helpOpen {
		content = m.overlayCentered(content, m.helpView())
	}

	// Composite the keybind editor centered over the content, if open.
	if m.keysEd != nil {
		content = m.overlayCentered(content, m.keysEd.View(m.cfg.Keys, m.contentHeight()-10))
	}

	// Composite the config overlay, if open. Its top edge is pinned rather
	// than centered: tabs differ in height, and centering each one moved
	// the whole panel up and down as you switched.
	if m.settings != nil {
		box := m.settings.View(m.cfg)
		x, _ := m.centerOf(box)
		content = lipgloss.NewCompositor(
			lipgloss.NewLayer(content),
			lipgloss.NewLayer(box).X(x).Y(m.settings.TopY(m.cfg, m.height)).Z(1),
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

	if m.frame != nil {
		m.frame.content, m.frame.valid = content, true
	}
	v.Content = content
	return v
}

// overlayCentered composites box centered over content.
func (m Model) overlayCentered(content, box string) string {
	x, y := m.centerOf(box)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

// centerOf is the top-left cell of box when it is centered on screen.
func (m Model) centerOf(box string) (x, y int) {
	return max(0, (m.width-lipgloss.Width(box))/2), max(0, (m.height-lipgloss.Height(box))/2)
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
	all := m.previewSplit(cur)
	total := len(all)
	// Copy: the clip aliases the cached split, and the padding below writes
	// to it, which would corrupt the cache for the next frame.
	lines := make([]string, height)
	copy(lines, clipLines(all, m.previewScroll, height))
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
	return strings.Join(clipLines(strings.Split(s, "\n"), offset, n), "\n")
}

// clipLines returns at most n lines starting at offset, without copying the
// rest: the preview is long and this runs every frame.
func clipLines(lines []string, offset, n int) []string {
	if n <= 0 {
		return nil
	}
	offset = clamp(offset, 0, len(lines))
	lines = lines[offset:]
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
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
	row := lipgloss.JoinHorizontal(lipgloss.Bottom, m.tabLabels()...)
	if tag := m.versionTag(); tag != "" {
		gap := m.width - lipgloss.Width(row) - lipgloss.Width(tag) - 2
		if gap > 0 {
			row += strings.Repeat(" ", gap) + tag
		}
	}
	return m.theme.tabBar.Width(m.width).Render(row)
}

// tabLabels renders each view's tab label, in view order. renderTabs lays
// them side by side from column 0, which tabAt relies on to hit-test clicks.
func (m Model) tabLabels() []string {
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
	return labels
}

// windowTitle names the terminal window/tab after the focused view, so a
// multiplexer shows "agenda · PRs" rather than the raw argv.
func (m Model) windowTitle() string {
	if len(m.views) == 0 || m.current >= len(m.views) {
		return "agenda"
	}
	return "agenda · " + m.views[m.current].Title()
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
	case "PRs", "Reviews":
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
	case "Reviews":
		return ui.Glyph(ui.IconTabReviews, "")
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
	line("click", "select · double-click runs enter")

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
	// With the keys in the preview the list-scoped hints (follow a
	// reference, filter) are about the pane you are not using, so they
	// give way, and a pill names the pane that is listening.
	focus := m.previewFocus()
	mode := ""
	if focus != "" {
		mode = m.theme.footerMode.Render(strings.ToUpper(focus)) + " "
	}
	var follow []key.Binding
	if len(m.currentRefs()) > 0 && focus == "" {
		follow = append(follow, m.keys.Follow)
	}
	filter := []key.Binding{m.keys.Filter}
	if focus != "" {
		filter = nil
	}

	// Zooming an already-hidden preview makes no sense, so the zoom hint
	// only shows while the preview is in view (v brings it back first).
	pane := []key.Binding{m.keys.TogglePreview}
	if !m.previewHidden && !m.floating() {
		pane = append(pane, m.keys.Zoom)
	}
	full := append(append(append(append([]key.Binding{}, view...), follow...),
		filter...), pane...)
	full = append(full, m.keys.NextView, m.keys.PreviewUp, m.keys.Refresh,
		m.keys.Config, m.keys.Help, m.keys.Quit)

	status := m.views[m.current].Status()

	// Hidden: only what you cannot do without, on the right. The hotkeys
	// are learnable; a waiting error and the way to the help are not.
	if !m.cfg.FooterEnabled() {
		parts := []string{}
		if hint := m.issuesHint(); hint != "" {
			parts = append(parts, hint)
		}
		if status != "" {
			parts = append(parts, status)
		}
		parts = append(parts, m.theme.footerKey.Render(m.keys.Help.Keys()[0])+" "+
			m.theme.footerDesc.Render("help"))
		right := strings.Join(parts, m.theme.footerSep.Render())
		gap := max(1, m.width-lipgloss.Width(right))
		return m.theme.footer.Width(m.width).Render(
			strings.Repeat(" ", gap) + right)
	}

	if hint := m.issuesHint(); hint != "" {
		if status != "" {
			status = hint + m.theme.footerSep.Render() + status
		} else {
			status = hint
		}
	}
	left := mode + m.footerLine(full)
	if lipgloss.Width(left)+lipgloss.Width(status)+1 > m.width {
		compact := view
		if len(compact) > 4 {
			compact = compact[:4]
		}
		compact = append(append(append(append([]key.Binding{}, compact...), follow...),
			filter...), pane...)
		compact = append(compact, m.keys.Help, m.keys.Quit)
		left = mode + m.footerLine(compact)
	}

	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(status))
	return m.theme.footer.Width(m.width).Render(
		left + strings.Repeat(" ", gap) + status,
	)
}
