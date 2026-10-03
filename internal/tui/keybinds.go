package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The keybind editor: every action grouped by scope, editable in place with
// collision detection. Edits write to config.yml (keys.<scope>.<action>) and
// global-scope changes apply live; view scopes re-resolve on restart.

// keyEntry describes one bindable action. The defaults here MUST match the
// bind() calls in the views and newKeys; this registry is the editor's and
// help's source of truth for what exists and what the fallback is.
type keyEntry struct {
	scope, action, label string
	def                  []string
}

func keyRegistry() []keyEntry {
	return []keyEntry{
		{"global", "next_view", "next view", []string{"tab", "L"}},
		{"global", "prev_view", "prev view", []string{"shift+tab", "H"}},
		{"global", "refresh", "refresh", []string{"ctrl+r"}},
		{"global", "quit", "quit", []string{"q", "ctrl+c"}},
		{"global", "help", "help", []string{"?"}},
		{"global", "follow", "follow reference", []string{"l"}},
		{"global", "filter", "field filter", []string{"f"}},
		{"global", "zoom", "zoom preview", []string{"z"}},
		{"global", "toggle_preview", "show/hide preview", []string{"v"}},
		{"global", "messages", "message log", []string{"!"}},
		{"global", "config", "config overlay", []string{"ctrl+s"}},
		{"global", "preview_up", "preview up", nil},
		{"global", "preview_down", "preview down", nil},
		{"global", "preview_pgup", "preview page up", nil},
		{"global", "preview_pgdn", "preview page down", nil},
		{"list", "up", "up", []string{"up", "k"}},
		{"list", "down", "down", []string{"down", "j"}},
		{"list", "top", "top", []string{"g", "home"}},
		{"list", "bottom", "bottom", []string{"G", "end"}},
		{"list", "half_up", "half page up", []string{"ctrl+u"}},
		{"list", "half_down", "half page down", []string{"ctrl+d"}},
		{"list", "jump_up", "jump up", []string{"shift+up", "pgup"}},
		{"list", "jump_down", "jump down", []string{"shift+down", "pgdown"}},
		{"list", "quick_filter", "quick filter", []string{"/"}},
		{"list", "clear_filter", "clear filter", []string{"esc"}},
		{"prs", "open", "open in browser", []string{"enter"}},
		{"prs", "copy_url", "copy url", []string{"y"}},
		{"prs", "diff", "diff", []string{"d"}},
		{"prs", "edit_filter", "edit the search", []string{"F"}},
		{"prs", "comments", "comments pane", []string{"c"}},
		{"prs", "review", "review popup", []string{"r"}},
		{"prs", "comment", "new PR comment", []string{"C"}},
		{"prs", "reply", "reply to thread", []string{"R"}},
		{"prs", "resolve", "resolve thread", []string{"X"}},
		{"prs", "next_thread", "next thread / failed job", []string{"]"}},
		{"prs", "prev_thread", "prev thread / failed job", []string{"["}},
		{"prs", "jobs", "jobs pane", []string{"t"}},
		{"prs", "open_job", "open job in browser", []string{"o"}},
		{"prs", "job_log", "job log in pager", []string{"p"}},
		{"prs", "rerun", "rerun jobs popup", []string{"x"}},
		{"prs", "sort", "sort", []string{"s"}},
		{"prs", "reverse", "reverse sort", []string{"S"}},
		{"prs", "toggle_review", "review-requested section", []string{"w"}},
		{"prs", "expand", "expand summary", []string{"e"}},
		{"sessions", "resume", "resume", []string{"enter"}},
		{"sessions", "sort", "sort", []string{"s"}},
		{"sessions", "reverse", "reverse sort", []string{"S"}},
		{"sessions", "agents", "agents toggle", []string{"a"}},
		{"sessions", "delete", "delete", []string{"d"}},
		{"sessions", "expand", "expand preview", []string{"E"}},
		{"linear", "nav", "nav pane", []string{"ctrl+p"}},
		{"linear", "mine", "only mine (project source)", []string{"m"}},
		{"linear", "comments", "comments in preview", []string{"c"}},
		{"linear", "open", "open in browser", []string{"enter"}},
		{"linear", "copy_url", "copy url", []string{"y"}},
		{"linear", "copy_branch", "copy branch", []string{"b"}},
		{"linear", "sort", "sort", []string{"s"}},
		{"linear", "reverse", "reverse sort", []string{"S"}},
	}
}

// keysFor resolves an entry's current binding from config overrides.
func keysFor(km config.Keymap, e keyEntry) []string {
	return km.Of(e.scope, e.action, e.def...)
}

// scopesCollide reports whether a key bound in scope a can shadow one in
// scope b: same scope always, and "global" and "list" apply inside every
// view.
func scopesCollide(a, b string) bool {
	return a == b || a == "global" || b == "global" || a == "list" || b == "list"
}

// findCollision returns the entry (other than target) that already binds key
// in a colliding scope, if any.
func findCollision(km config.Keymap, target keyEntry, key string) (keyEntry, bool) {
	// 1..9 jump to a view by tab position, handled by the chrome before any
	// view sees the key; a binding on a digit would be dead.
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		return keyEntry{scope: "global", action: "view_jump", label: "jump to view 1..9"}, true
	}
	for _, e := range keyRegistry() {
		if e.scope == target.scope && e.action == target.action {
			continue
		}
		if !scopesCollide(e.scope, target.scope) {
			continue
		}
		for _, k := range keysFor(km, e) {
			if k == key {
				return e, true
			}
		}
	}
	return keyEntry{}, false
}

// keybindEditor is the ctrl+s → keybinds overlay.
type keybindEditor struct {
	rows      []keyEntry
	cursor    int
	capturing bool
	errMsg    string
	flash     string
}

func newKeybindEditor() *keybindEditor {
	return &keybindEditor{rows: keyRegistry()}
}

// keybindChange is one committed edit: the entry and its new key list (empty
// disables the action; the default list clears back to stock behavior).
type keybindChange struct {
	entry keyEntry
	keys  []string
}

// Update handles one key. Returns a committed change (nil for navigation)
// and whether the editor closed.
func (o *keybindEditor) Update(msg tea.KeyMsg, km config.Keymap) (*keybindChange, bool) {
	e := o.rows[o.cursor]

	if o.capturing {
		o.capturing = false
		k := msg.String()
		if k == "esc" {
			return nil, false
		}
		if other, clash := findCollision(km, e, k); clash {
			o.errMsg = fmt.Sprintf("%q is already %s (%s)", k, other.label, other.scope)
			return nil, false
		}
		o.errMsg, o.flash = "", fmt.Sprintf("%s → %s", e.label, k)
		return &keybindChange{entry: e, keys: []string{k}}, false
	}

	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "up", "k":
		o.cursor = max(0, o.cursor-1)
	case "down", "j":
		o.cursor = min(len(o.rows)-1, o.cursor+1)
	case "enter":
		o.capturing, o.errMsg, o.flash = true, "", ""
	case "backspace", "x":
		o.flash = e.label + " disabled"
		return &keybindChange{entry: e, keys: []string{}}, false
	case "r":
		o.flash = e.label + " reset to default"
		return &keybindChange{entry: e, keys: e.def}, false
	}
	return nil, false
}

// View renders the editor, windowed to maxRows visible entries.
func (o *keybindEditor) View(km config.Keymap, maxRows int) string {
	labelW := 0
	for _, e := range o.rows {
		if len(e.label) > labelW {
			labelW = len(e.label)
		}
	}

	// Window the rows around the cursor.
	maxRows = max(8, maxRows)
	start := 0
	if o.cursor >= maxRows {
		start = o.cursor - maxRows + 1
	}
	end := min(len(o.rows), start+maxRows)

	var b strings.Builder
	b.WriteString(ui.Bold.Render("Keybinds"))
	b.WriteString("\n\n")
	lastScope := ""
	for i := start; i < end; i++ {
		e := o.rows[i]
		if e.scope != lastScope {
			b.WriteString(ui.Dim.Render(e.scope))
			b.WriteByte('\n')
			lastScope = e.scope
		}
		cursor := "  "
		if i == o.cursor {
			cursor = ui.Accent.Render("› ")
		}
		val := strings.Join(keysFor(km, e), " / ")
		if val == "" {
			val = ui.Faint.Render("(disabled)")
		}
		if i == o.cursor {
			if o.capturing {
				val = ui.Yellow.Render("press a key… (esc cancels)")
			} else {
				val = ui.Accent.Render(val)
			}
		}
		fmt.Fprintf(&b, "%s%-*s  %s\n", cursor, labelW, e.label, val)
	}
	if end < len(o.rows) {
		b.WriteString(ui.Faint.Render(fmt.Sprintf("… %d more below", len(o.rows)-end)))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	switch {
	case o.errMsg != "":
		b.WriteString(ui.Red.Render(o.errMsg))
		b.WriteByte('\n')
	case o.flash != "":
		b.WriteString(ui.Green.Render(o.flash))
		b.WriteByte('\n')
	}
	b.WriteString(ui.Dim.Render("enter rebind · x disable · r reset · esc back"))
	b.WriteByte('\n')
	b.WriteString(ui.Faint.Render("global keys apply now; view keys apply on restart"))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Render(b.String())
}
