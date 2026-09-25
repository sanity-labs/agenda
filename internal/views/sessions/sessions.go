// Package sessions is agenda's agent-sessions view. It lists Claude Code,
// Codex, and Antigravity sessions across the filesystem, previews their
// conversation, and resumes the selected one in its original directory. This
// is a Go port of the user's Python `sessions` tool.
package sessions

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/store"
	"github.com/sanity-labs/agenda/internal/ui"
)

func (s session) toolStyle() lipgloss.Style {
	switch s.Tool {
	case toolCodex:
		return ui.Green
	case toolAgy:
		return ui.Blue
	default:
		return ui.Magenta
	}
}

func (s session) titleOr() string {
	if s.Title == "" {
		return "(no prompt)"
	}
	return s.Title
}

// Selectable implements ui.NonSelectable: group headers never hold the cursor.
func (s session) Selectable() bool { return s.Separator == "" }

// isAgent reports whether this is a programmatic (SDK/command/hook-spawned)
// session rather than one the user typed interactively. Only Claude logs carry
// an entrypoint; "cli" is interactive, anything else (e.g. "sdk-py") is an agent
// session. Codex/Antigravity have no entrypoint, so they're never agents.
func (s session) isAgent() bool {
	return s.Entrypoint != "" && s.Entrypoint != "cli"
}

// defaultFields is the field scope a fresh Sessions view filters on. It omits
// "text" (the conversation body) so full-text search is off by default — the
// user opts in by toggling the "text" field on in the filter modal (f).
var defaultFields = []string{"tool", "cwd", "title", "model"}

// Filter is the identity key used to preserve the selection across re-sorts and
// re-filters (see ui.List). It is NOT used for match testing — that goes through
// Fields() so each field matches by its own rule (see ui.List.itemMatches). The
// body is deliberately excluded here: it's huge and would make a poor identity
// key.
func (s session) Filter() string {
	if s.Separator != "" {
		return "\x00sep:" + s.Separator
	}
	return fmt.Sprintf("%s %s %s %s", s.Tool, shortenPath(s.Cwd), s.Title, s.Model)
}

func (s session) Fields() []ui.Field {
	if s.Separator != "" {
		return nil
	}
	return []ui.Field{
		{Name: "tool", Text: string(s.Tool)},
		{Name: "cwd", Text: shortenPath(s.Cwd)},
		{Name: "title", Text: s.Title},
		{Name: "model", Text: s.Model},
		{Name: "text", Text: s.Body, Prose: true},
	}
}

func (s session) Render(width int, selected bool, hl ui.Highlighter) string {
	if s.Separator != "" {
		return ui.GroupHeader(s.Separator, width)
	}
	// Glyph column: the agent's Nerd Font icon (claude/codex/antigravity)
	// instead of its spelled-out name. Programmatic (agent) sessions get a muted
	// gear prefix so they read as a distinct class from human ones.
	glyphs := ui.AgentIcon(string(s.Tool))
	if s.isAgent() {
		glyphs = ui.Dim.Render("⚙") + glyphs
	}

	// metaPlain (width measurement) and metaStyled must carry the same text; the
	// rare session that spawned agent sub-sessions gets a "spawned N" hint.
	cwd := shortenPath(s.Cwd)
	metaStyled := ui.Cyan.Render(cwd)
	if s.Spawned > 0 {
		hint := fmt.Sprintf("⤷ spawned %d", s.Spawned)
		cwd += "  " + hint
		metaStyled += "  " + ui.Dim.Render(hint)
	}

	right := ui.Yellow.Render(strconv.Itoa(s.Msgs)) + "  " + ui.Dim.Render(ui.Age(s.Updated))
	if c := fmtCost(s.Cost); c != "" {
		right = ui.Green.Bold(true).Render(c) + "  " + right
	}

	return ui.TwoLineRow(width, selected, glyphs, cwd, metaStyled, right, s.titleOr(), hl)
}

// --- sorting ----------------------------------------------------------------

type sortMode int

const (
	sortRecent sortMode = iota
	sortCwd
	sortTool
	sortMsgs
	sortCost
)

var sortOrder = []sortMode{sortRecent, sortCwd, sortTool, sortMsgs, sortCost}
var sortName = map[sortMode]string{
	sortRecent: "recent", sortCwd: "cwd", sortTool: "tool",
	sortMsgs: "msgs", sortCost: "cost",
}

// groupLabelFn returns the swimlane label for a sort mode. msgs has no
// sensible buckets, so it stays flat (nil) even with grouping on.
func groupLabelFn(mode sortMode) func(session) string {
	switch mode {
	case sortRecent:
		return func(s session) string { return ui.TimeBucket(s.Updated) }
	case sortCwd:
		return func(s session) string { return shortenPath(s.Cwd) }
	case sortTool:
		return func(s session) string { return string(s.Tool) }
	default:
		return nil
	}
}

// sortSessions returns a sorted copy of in. When rev is set the comparison is
// negated, which flips the whole ordering — primary key and tie-breaks alike —
// so "recent" becomes oldest-first and "msgs" shortest-first. Equal items keep
// their original relative order either way.
func sortSessions(in []session, mode sortMode, rev bool) []session {
	out := make([]session, len(in))
	copy(out, in)
	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch mode {
		case sortCwd:
			if a.Cwd != b.Cwd {
				return strings.ToLower(a.Cwd) < strings.ToLower(b.Cwd)
			}
			return a.Updated.After(b.Updated)
		case sortTool:
			if a.Tool != b.Tool {
				return a.Tool < b.Tool
			}
			return a.Updated.After(b.Updated)
		case sortMsgs:
			return a.Msgs > b.Msgs
		case sortCost:
			if a.Cost != b.Cost {
				return a.Cost > b.Cost // most expensive first
			}
			return a.Updated.After(b.Updated)
		default: // recent
			return a.Updated.After(b.Updated)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rev {
			return less(j, i)
		}
		return less(i, j)
	})
	return out
}

// --- messages ---------------------------------------------------------------

type loadedMsg []session
type resumedMsg struct{}

// --- view -------------------------------------------------------------------

type View struct {
	list     ui.List[session]
	raw      []session
	sort     sortMode
	rev      bool // sort order reversed
	grouping bool // swimlanes derived from the active sort
	store    *store.Store

	showAgents bool // when false, programmatic (SDK/agent) sessions are hidden
	confirmDel bool // armed delete: next y/enter deletes the selected session
	loading    bool

	// expandExtra is how many turns beyond the base window to show in the preview,
	// added by shift+e. expandKey ties it to the selected session so it resets
	// when the selection changes.
	expandExtra int
	expandKey   string
	// hasHiddenTurns caches whether the last-rendered preview had turns above the
	// fold, so Bindings() can offer expand without re-reading the file each frame.
	hasHiddenTurns bool

	// turnCache memoizes the parsed conversation turns of the selected session, so
	// the preview isn't re-read and re-parsed on every render/scroll tick (a real
	// cost on long sessions). Keyed by file path; invalidated when the selection
	// changes to a different path.
	turnCache    []turn
	turnCacheKey string

	listW, prevW, height int

	keys viewKeys
}

type viewKeys struct {
	Resume key.Binding
	Sort   key.Binding
	Rev    key.Binding
	Agents key.Binding
	Delete key.Binding
	Expand key.Binding
}

func New(km config.Keymap, st *store.Store) *View {
	bind := func(action, desc string, def ...string) key.Binding {
		return ui.Bind(km.Of("sessions", action, def...), "", desc)
	}
	v := &View{
		store:   st,
		list:    ui.NewList[session](),
		loading: true,
		keys: viewKeys{
			Resume: bind("resume", "resume", "enter"),
			Sort:   bind("sort", "sort", "s"),
			Rev:    bind("reverse", "reverse", "S"),
			Agents: bind("agents", "agents", "a"),
			Delete: bind("delete", "delete", "d"),
			Expand: bind("expand", "expand", "E"),
		},
	}
	v.list.SetRowHeight(2) // two-line rows: cwd + title
	v.list.Rebind(func(a string, d ...string) []string { return km.Of("list", a, d...) })
	// Default filter scope excludes the "text" body field; the user enables it in
	// the filter modal (f) to search inside conversations.
	v.list.SetEnabledFields(defaultFields)
	return v
}

func (v *View) Title() string { return "Sessions" }

func (v *View) Init() tea.Cmd {
	v.loading = true
	return v.fetch()
}

func (v *View) Loading() bool { return v.loading }

func (v *View) fetch() tea.Cmd {
	return func() tea.Msg { return loadedMsg(collect()) }
}

// applyView filters raw by the agent toggle, then sorts, then hands the result
// to the list. Cost totals (see statusText) are computed over raw regardless, so
// hiding agent sessions never hides their spend. With grouping on and a sort
// that declares a dimension, swimlane headers land wherever the label changes,
// so with agents shown each partition gets its own lanes.
func (v *View) applyView() {
	shown := v.raw
	if !v.showAgents {
		shown = make([]session, 0, len(v.raw))
		for _, s := range v.raw {
			if !s.isAgent() {
				shown = append(shown, s)
			}
		}
	}
	sorted := sortSessions(shown, v.sort, v.rev)
	if v.showAgents {
		// Group: human sessions first, then agent sessions, each block keeping the
		// active sort order. A stable partition preserves that order within groups.
		humans := make([]session, 0, len(sorted))
		agents := make([]session, 0, len(sorted))
		for _, s := range sorted {
			if s.isAgent() {
				agents = append(agents, s)
			} else {
				humans = append(humans, s)
			}
		}
		sorted = append(humans, agents...)
	}
	if v.grouping {
		if label := groupLabelFn(v.sort); label != nil {
			sorted = ui.InsertGroups(sorted, label, func(l string) session { return session{Separator: l} })
		}
	}
	v.list.SetItems(sorted)
}

func (v *View) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loadedMsg:
		v.loading = false
		v.raw = []session(msg)
		v.turnCacheKey = "" // a rescan may have changed the selected session's file
		v.applyView()
		v.publishMentions()
		return nil
	case resumedMsg:
		// Resuming likely appended new turns; rescan so order/age stay accurate.
		return v.fetch()
	case ui.GroupingMsg:
		v.grouping = bool(msg)
		v.applyView()
		return nil
	case tea.KeyMsg:
		// A pending delete confirmation captures the next key.
		if v.confirmDel {
			switch msg.String() {
			case "y", "enter":
				v.confirmDel = false
				return v.deleteSelected()
			default:
				v.confirmDel = false // any other key cancels
				return nil
			}
		}
		before := v.list.Selected().Path
		if consumed, cmd := v.list.Update(msg); consumed {
			if v.list.Selected().Path != before {
				// Moving on ends a transient preview reveal.
				return tea.Batch(cmd, ui.ConcealPreview)
			}
			return cmd
		}
		if v.list.Filtering() {
			return nil
		}
		switch {
		case key.Matches(msg, v.keys.Delete):
			if v.list.Selected().Path != "" {
				v.confirmDel = true
			}
			return nil
		case key.Matches(msg, v.keys.Resume):
			return v.resume()
		case key.Matches(msg, v.keys.Sort):
			v.sort = sortOrder[(int(v.sort)+1)%len(sortOrder)]
			v.applyView()
			return nil
		case key.Matches(msg, v.keys.Agents):
			v.showAgents = !v.showAgents
			v.applyView()
			return nil
		case key.Matches(msg, v.keys.Expand):
			// Reveal 14 more turns in the preview for the selected session.
			if s := v.list.Selected(); s.Path != "" {
				v.expandKey = s.Path
				v.expandExtra += maxTurns
			}
			return nil
		case key.Matches(msg, v.keys.Rev):
			v.rev = !v.rev
			v.applyView()
			return nil
		}
	}
	return nil
}

// resume launches the selected agent CLI in the session's directory, suspending
// agenda until it exits.
func (v *View) resume() tea.Cmd {
	s := v.list.Selected()
	if s.SessionID == "" {
		return nil
	}
	var c *exec.Cmd
	switch s.Tool {
	case toolCodex:
		c = exec.Command("codex", "resume", s.SessionID)
	case toolAgy:
		c = exec.Command("agy", "--conversation", s.SessionID)
	default:
		c = exec.Command("claude", "--resume", s.SessionID)
	}
	if s.Cwd != "" {
		if fi, err := os.Stat(s.Cwd); err == nil && fi.IsDir() {
			c.Dir = s.Cwd
		}
	}
	return tea.ExecProcess(c, func(error) tea.Msg { return resumedMsg{} })
}

// deleteSelected removes the selected session's log file from disk and rescans.
// For Claude/Codex that's the single .jsonl; for Antigravity the conversation
// .db (its brain/ transcript dir is left — harmless, and keyed by a different id).
func (v *View) deleteSelected() tea.Cmd {
	s := v.list.Selected()
	if s.Path == "" {
		return nil
	}
	_ = os.Remove(s.Path)
	return v.fetch() // rescan so the row disappears and totals update
}

// ScrollList moves the list selection by n rows (mouse wheel). It disarms a
// pending delete, so a y typed afterwards can't hit a row scrolled onto.
func (v *View) ScrollList(n int) tea.Cmd {
	v.confirmDel = false
	before := v.list.Selected().Path
	v.list.ScrollBy(n)
	return v.mouseMoved(before)
}

// ClickList selects the row under a click in the list column; y is relative
// to the column top, whose first line is the header.
func (v *View) ClickList(_, y int) (bool, tea.Cmd) {
	before := v.list.Selected().Path
	if !v.list.ClickAt(y - 1) {
		return false, nil
	}
	return true, v.mouseMoved(before)
}

// Activate resumes the selected session (a double-click).
func (v *View) Activate() tea.Cmd { return v.resume() }

// mouseMoved ends a transient preview reveal once the selection moves on.
func (v *View) mouseMoved(before string) tea.Cmd {
	if v.list.Selected().Path == before {
		return nil
	}
	return ui.ConcealPreview
}

func (v *View) SetSize(listW, prevW, h int) {
	v.listW, v.prevW, v.height = listW, prevW, h
	v.list.SetSize(listW, max(1, h-1))
}

func (v *View) ListView() string {
	header := v.list.FilterLine()
	if header == "" {
		header = ui.Faint.Render(v.statusText())
	}
	return header + "\n" + v.list.View()
}

func (v *View) statusText() string {
	if v.confirmDel {
		s := v.list.Selected()
		return ui.Yellow.Render(fmt.Sprintf("Delete %q? (y/n)", ui.Truncate(s.titleOr(), 50)))
	}
	if v.loading {
		return "Scanning sessions…"
	}
	// Cost is split by class over ALL sessions (not just the visible ones) so the
	// totals stay honest whether or not agent sessions are shown.
	var youCost, agentCost float64
	for _, s := range v.raw {
		if s.isAgent() {
			agentCost += s.Cost
		} else {
			youCost += s.Cost
		}
	}
	base := fmt.Sprintf("%d sessions · sort: %s%s", v.list.Total(), sortName[v.sort], ui.RevMarker(v.rev))
	if youCost > 0 {
		base += " · you " + fmtCost(youCost)
	}
	if agentCost > 0 {
		state := "hidden"
		if v.showAgents {
			state = "shown"
		}
		base += fmt.Sprintf(" · agents %s (%s, a)", fmtCost(agentCost), state)
	}
	return base
}

// maxTurns is the base number of most-recent turns the preview shows; shift+e
// reveals maxTurns more each press. When a text query is active the preview
// switches to a matches-only view instead of expanding the window.
const maxTurns = 14

// cachedTurns returns the parsed turns for session s, memoized by path so the
// preview (rendered several times per frame, and on every scroll tick) parses
// the file at most once per selection. A cheap fix for lag on long sessions.
func (v *View) cachedTurns(s session) []turn {
	if s.Path == v.turnCacheKey {
		return v.turnCache
	}
	v.turnCache = conversationTurns(s.Path, s.Tool)
	v.turnCacheKey = s.Path
	return v.turnCache
}

// matchCount counts occurrences of the active query in the selected session's
// (cached, in-memory) body, for the footer status. 0 when there's no query or no
// match. Uses s.Body rather than re-reading the file so it's cheap to call every
// frame.
func (v *View) matchCount(s session) int {
	q := strings.TrimSpace(v.list.Query())
	if q == "" || s.Body == "" {
		return 0
	}
	hay := s.Body
	if !v.list.CaseSensitive() {
		hay, q = strings.ToLower(hay), strings.ToLower(q)
	}
	return strings.Count(hay, q)
}

func (v *View) PreviewView() string {
	s := v.list.Selected()
	if s.Path == "" {
		return ui.Faint.Render("No session selected.")
	}

	var b strings.Builder
	b.WriteString(ui.AgentIcon(string(s.Tool)))
	b.WriteString("  ")
	b.WriteString(s.toolStyle().Bold(true).Render(strings.ToUpper(string(s.Tool))))
	b.WriteString("  ")
	header := fmt.Sprintf("%s · %d msgs", s.Updated.Format("2006-01-02 15:04"), s.Msgs)
	if s.Model != "" {
		header += " · " + s.Model
	}
	b.WriteString(ui.Dim.Render(header))
	b.WriteString("\n")
	cost := fmtCost(s.Cost)
	if cost == "" {
		cost = "–"
	}
	b.WriteString(ui.Dim.Render("cost: "))
	b.WriteString(ui.Green.Bold(true).Render(cost))
	b.WriteString("\n")
	// Token usage breakdown, under cost (Claude only; zero for other tools).
	if s.InTok+s.OutTok+s.CacheTok > 0 {
		b.WriteString(ui.Dim.Render(fmt.Sprintf("tokens: in %s · out %s · cache %s",
			fmtTokens(s.InTok), fmtTokens(s.OutTok), fmtTokens(s.CacheTok))))
		b.WriteString("\n")
	}
	b.WriteString(ui.Cyan.Render(shortenPath(s.Cwd)))
	b.WriteString("\n")
	b.WriteString(ui.Faint.Render(s.SessionID))
	b.WriteString("\n")
	b.WriteString(ui.Dim.Render(strings.Repeat("─", min(v.prevW, 60))))
	b.WriteString("\n")

	turns := v.cachedTurns(s)
	if len(turns) == 0 {
		b.WriteString(ui.Faint.Render("(no conversation content)"))
		return b.String()
	}

	hl := ui.Highlighter{Query: v.list.Query(), CaseSensitive: v.list.CaseSensitive()}
	wrap := lipgloss.NewStyle().Width(max(20, v.prevW))
	writeTurn := func(t turn) {
		label, style := "● ai ", ui.Blue
		if t.role == "user" {
			label, style = "▶ you", ui.Green
		}
		b.WriteString(style.Render(label))
		b.WriteByte(' ')
		b.WriteString(wrap.Render(hl.HighlightSubstr(ui.Truncate(t.text, 600))))
		b.WriteString("\n\n")
	}

	// Matches-only mode: with an active query, render just the turns that contain
	// it (cheap even for very long sessions) rather than the whole tail. Without a
	// query, show the most-recent window, growable with shift+e.
	if q := v.queryLower(); q != "" {
		shown := 0
		for _, t := range turns {
			if strings.Contains(v.fold(t.text), q) {
				writeTurn(t)
				shown++
			}
		}
		v.hasHiddenTurns = false // matches-only: nothing to expand into
		if shown == 0 {
			b.WriteString(ui.Faint.Render("(no matching turns in this session)"))
		}
		return b.String()
	}

	start := len(turns) - maxTurns - v.expandFor(s)
	if start < 0 {
		start = 0
	}
	v.hasHiddenTurns = start > 0
	if start > 0 {
		b.WriteString(ui.Faint.Render(fmt.Sprintf("… %d earlier turns (⇧e to expand) …", start)))
		b.WriteString("\n\n")
	}
	for _, t := range turns[start:] {
		writeTurn(t)
	}
	return b.String()
}

// expandFor returns the manual (shift+e) extra-turn count for session s.
func (v *View) expandFor(s session) int {
	if s.Path == v.expandKey {
		return v.expandExtra
	}
	return 0
}

// queryLower returns the trimmed filter query, lowercased unless a case-sensitive
// match is active. Empty when there's no query.
func (v *View) queryLower() string {
	q := strings.TrimSpace(v.list.Query())
	if q != "" && !v.list.CaseSensitive() {
		q = strings.ToLower(q)
	}
	return q
}

// fold lowercases s unless case-sensitive matching is active, matching queryLower.
func (v *View) fold(s string) string {
	if v.list.CaseSensitive() {
		return s
	}
	return strings.ToLower(s)
}

func (v *View) Bindings() []key.Binding {
	b := []key.Binding{v.keys.Resume, v.keys.Sort, v.keys.Rev, v.keys.Agents, v.keys.Delete}
	// Offer expand only when the last-rendered preview had turns above the fold
	// (computed in PreviewView, which renders earlier in the same frame).
	if v.hasHiddenTurns {
		b = append(b, v.keys.Expand)
	}
	return b
}

func (v *View) Status() string {
	// Surface the match count for the selected session so the user knows there's
	// something to expand into.
	if n := v.matchCount(v.list.Selected()); n > 0 {
		return ui.Green.Render(fmt.Sprintf("%d matches", n)) + ui.Dim.Render(" · "+v.statusText())
	}
	return ui.Dim.Render(v.statusText())
}

func (v *View) InputActive() bool { return v.list.Filtering() || v.confirmDel }

func (v *View) Fields() []string { return v.list.FieldNames() }

func (v *View) FilterState() (string, []string, bool) {
	return v.list.Query(), v.list.EnabledFields(), v.list.CaseSensitive()
}

func (v *View) SetFilter(query string, enabled []string, caseSensitive bool) {
	v.list.SetEnabledFields(enabled)
	v.list.SetCaseSensitive(caseSensitive)
	v.list.SetQuery(query)
}

func (v *View) PreviewKey() string { return v.list.Selected().Path }

// publishMentions rebuilds the shared reverse index (entity -> sessions that
// mention it) so the PRs and Linear views can list the sessions referencing a
// given PR or issue.
func (v *View) publishMentions() {
	if v.store == nil {
		return
	}
	index := map[string][]store.SessionRef{}
	for _, s := range v.raw {
		for _, mn := range s.Mentions {
			key := store.Key(mn.Kind, mn.ID)
			index[key] = append(index[key], store.SessionRef{
				Path:    s.Path,
				Tool:    string(s.Tool),
				Title:   s.titleOr(),
				Cwd:     shortenPath(s.Cwd),
				Snippet: mn.Snippet,
			})
		}
	}
	v.store.SetSessionMentions(index)
}

// Refs implements ui.Referencer: the Linear issues and PRs this session
// mentions, rendered like the other views' references — issue titles and PR
// status icons/titles are sourced from the shared store.
func (v *View) Refs() []ui.Ref {
	var refs []ui.Ref
	for _, mn := range v.list.Selected().Mentions {
		switch mn.Kind {
		case "linear":
			var title, url string
			if v.store != nil {
				if iss, ok := v.store.Issue(mn.ID); ok {
					title, url = iss.Title, iss.URL
				}
			}
			refs = append(refs, ui.IssueRef(mn.ID, title, url))
		case "pr":
			repo, num, _ := ui.ParsePRURL(mn.ID)
			var pr store.PR
			if v.store != nil {
				pr, _ = v.store.PR(mn.ID)
			}
			refs = append(refs, ui.PRRef(pr, repo, num, pr.Title, mn.ID))
		}
	}
	return refs
}

// RefKind / HasRef / SelectRef implement ui.RefTarget so other views can jump
// to a session here. Sessions are keyed by file path.
func (v *View) RefKind() string { return "session" }

func matchPath(path string) func(session) bool {
	return func(s session) bool { return s.Path == path }
}

func (v *View) HasRef(id string) bool    { return v.list.Any(matchPath(id)) }
func (v *View) SelectRef(id string) bool { return v.list.Select(matchPath(id)) }
