// Package linear is agenda's Linear-issues view. It lists the issues assigned
// to the authenticated user and previews the selected one.
//
// Data comes from the Linear GraphQL API over HTTP, authenticated with a
// personal API key (lin_api_...) from agenda's config. When no token is set
// the view renders a short setup hint instead of fetching.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sanity-labs/agenda/internal/cache"
	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/notify"
	"github.com/sanity-labs/agenda/internal/store"
	"github.com/sanity-labs/agenda/internal/ui"
)

const endpoint = "https://api.linear.app/graphql"

// hexColor returns a lipgloss style whose foreground is the given Linear hex
// color (which may or may not include a leading '#'), falling back to ui.Dim.
func hexStyle(hex string) lipgloss.Style {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return ui.Dim
	}
	return ui.Fg("#" + hex)
}

// --- data -------------------------------------------------------------------

type label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// issue is one row. A row with a non-empty Separator is a group header, not
// an issue.
type issue struct {
	Separator string `json:"-"`
	// HideStatus / HideProject suppress the row's status or project text when
	// the list is grouped by that dimension, where the lane header already
	// says it.
	HideStatus  bool `json:"-"`
	HideProject bool `json:"-"`
	// ShowLabels adds a label column, set at assembly when the preview pane
	// is off and the row is wide enough to spare the space.
	ShowLabels bool `json:"-"`
	// Inbox rows represent a notification about the issue rather than the
	// issue itself: who did what (InboxEvent/InboxActor) and whether it is
	// still unread. UpdatedAt then carries the notification time.
	InboxEvent  string `json:"-"`
	InboxActor  string `json:"-"`
	InboxUnread bool   `json:"-"`
	// Fresh marks an issue that arrived since the last fetch, cleared when
	// you select it. Distinct from InboxUnread, which is Linear's own
	// notification state on an inbox row. FreshGutter reserves the column
	// even when read, so clearing a mark does not shift the row.
	Fresh         bool      `json:"-"`
	FreshGutter   bool      `json:"-"`
	Identifier    string    `json:"identifier"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	Priority      int       `json:"priority"`
	PriorityLabel string    `json:"priorityLabel"`
	BranchName    string    `json:"branchName"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Description   string    `json:"description"`
	State         struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Color string `json:"color"`
	} `json:"state"`
	Team struct {
		Key string `json:"key"`
	} `json:"team"`
	Project struct {
		Name string `json:"name"`
	} `json:"project"`
	Assignee struct {
		DisplayName string `json:"displayName"`
	} `json:"assignee"`
	// Comments carries ids only, to count: Linear's CommentConnection has
	// no totalCount, so the list query asks for a page and whether there
	// is more.
	Comments struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	} `json:"comments"`
	Labels struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Attachments struct {
		Nodes []attachment `json:"nodes"`
	} `json:"attachments"`
}

// attachment is a Linear attachment; for GitHub PRs the metadata carries the
// PR's status (used as a fallback when the PRs view hasn't loaded the PR).
type attachment struct {
	URL        string `json:"url"`
	SourceType string `json:"sourceType"`
	Title      string `json:"title"`
	Metadata   struct {
		Draft        bool   `json:"draft"`
		Status       string `json:"status"` // open | inReview | merged | closed
		HasConflicts bool   `json:"hasConflicts"`
		Reviews      []struct {
			State string `json:"state"` // approved | changes_requested | ...
		} `json:"reviews"`
	} `json:"metadata"`
}

// toPR builds the store metadata a GitHub PR attachment implies. CI status
// isn't in Linear's data, so it stays unknown (no CI glyph).
func (a attachment) toPR() store.PR {
	p := store.PR{HasConflicts: a.Metadata.HasConflicts}
	switch {
	case a.Metadata.Status == "merged":
		p.State = store.PRMerged
	case a.Metadata.Status == "closed":
		p.State = store.PRClosed
	case a.Metadata.Draft:
		p.State = store.PRDraft
	default:
		p.State = store.PROpen
	}
	changes, approved := false, false
	for _, r := range a.Metadata.Reviews {
		switch r.State {
		case "changes_requested":
			changes = true
		case "approved":
			approved = true
		}
	}
	switch {
	case changes:
		p.Review = store.ReviewChanges
	case approved:
		p.Review = store.ReviewApproved
	case a.Metadata.Status == "inReview":
		p.Review = store.ReviewPending
	}
	return p
}

// resetToggles returns the per-issue toggles to the configured default. A
// toggle belongs to the issue it was pressed on.
func (v *View) resetToggles() {
	if v.togglesPersist {
		return
	}
	v.showComments = v.cfgShowComments
	v.commentsJumped = false
}

// Selectable implements ui.NonSelectable: group headers never hold the cursor.
func (i issue) Selectable() bool { return i.Separator == "" }

func (i issue) Filter() string {
	if i.Separator != "" {
		return "\x00sep:" + i.Separator
	}
	return fmt.Sprintf("%s %s %s %s", i.Identifier, i.State.Name, i.Project.Name, i.Title)
}

// Key is the row's identity across refreshes: the identifier, which a
// status or project change does not touch.
func (i issue) Key() string {
	if i.Separator != "" {
		return "\x00sep:" + i.Separator
	}
	return i.Identifier
}

func (i issue) Fields() []ui.Field {
	if i.Separator != "" {
		return nil
	}
	return []ui.Field{
		{Name: "id", Text: i.Identifier},
		{Name: "status", Text: i.State.Name},
		{Name: "title", Text: i.Title},
		{Name: "project", Text: i.Project.Name},
		{Name: "assignee", Text: i.Assignee.DisplayName},
	}
}

// priorityCell renders a one-glyph, color-coded priority indicator. Linear:
// 0 none, 1 urgent, 2 high, 3 medium, 4 low.
func (i issue) priorityCell() string {
	switch i.Priority {
	case 1:
		return ui.Red.Bold(true).Render("!")
	case 2:
		return ui.Yellow.Render("↑")
	case 3:
		return ui.Blue.Render("•")
	case 4:
		return ui.Dim.Render("↓")
	default:
		return ui.Dim.Render("·")
	}
}

const (
	// ageCellW pads the age so it forms a column instead of drifting.
	ageCellW = 4 // "999d"
	// commentsCellW is the icon and up to three digits, as in the PRs view.
	commentsCellW = 4
	// labelColReserve is the room kept for the metadata line. Lower than
	// the PRs view's: Linear labels run long, and the metadata truncates
	// with an ellipsis where labels would be dropped whole.
	labelColReserve = 30
)

// commentCount is how many comments the list query saw, and whether the
// page it asked for was full with more behind it.
func (i issue) commentCount() (n int, more bool) {
	return len(i.Comments.Nodes), i.Comments.PageInfo.HasNextPage
}

// commentsText is the comment-count cell's plain text, empty for none so a
// quiet issue shows nothing rather than a zero.
func (i issue) commentsText() string {
	n, more := i.commentCount()
	if n == 0 {
		return ""
	}
	if more {
		return ui.IconComment + strconv.Itoa(n) + "+"
	}
	return ui.IconComment + strconv.Itoa(n)
}

// pillsFor renders one pill per label, for the shared column packer.
func pillsFor(labels []label) []string {
	out := make([]string, len(labels))
	for i := range labels {
		out[i] = labelPills(labels[i : i+1])
	}
	return out
}

func (i issue) Render(width int, selected bool, hl ui.Highlighter) string {
	if i.Separator != "" {
		return ui.GroupHeader(i.Separator, width)
	}
	if i.InboxEvent != "" {
		return i.renderInboxRow(width, selected, hl)
	}
	glyphs := i.priorityCell()
	// An arrival since the last fetch leads with a bold blue dot, the one
	// palette colour distinct from the accent in every built-in theme; the
	// gutter stays reserved once read so rows do not shift on clearing.
	if i.FreshGutter {
		mark := strings.Repeat(" ", lipgloss.Width(ui.IconUnread))
		if i.Fresh {
			mark = ui.Blue.Bold(true).Render(ui.IconUnread)
		}
		glyphs = mark + " " + glyphs
	}

	// Metadata: state · identifier (· project), minus whatever the active
	// grouping's lane header already announces.
	plain, styled := "", ""
	if !i.HideStatus {
		plain = i.State.Name + "  "
		styled = hexStyle(i.State.Color).Render(i.State.Name) + "  "
	}
	plain += i.Identifier
	styled += ui.Cyan.Render(i.Identifier)
	// The project takes the slot the PRs view gives its issue number, so
	// it stands out from the dim run and follows the palette.
	if i.Project.Name != "" && !i.HideProject {
		plain += " · " + i.Project.Name
		styled += ui.Dim.Render(" · ") + ui.Yellow.Render(i.Project.Name)
	}
	// Who has it, always: an unassigned issue says so rather than reading
	// like one whose assignee did not fit.
	if who := i.Assignee.DisplayName; who != "" {
		plain += " · @" + who
		styled += ui.Dim.Render(" · @" + who)
	} else {
		plain += " · Unassigned"
		styled += ui.Faint.Render(" · Unassigned")
	}

	// Comments and age are fixed-width so they form columns; labels take
	// the space a hidden preview frees up, to their left.
	right := ui.PadCell(ui.Dim.Render(i.commentsText()), commentsCellW) + " " +
		ui.PadCell(ui.Dim.Render(ui.Age(i.UpdatedAt)), ageCellW)
	if i.ShowLabels {
		right = ui.LabelColumn(width, commentsCellW+ageCellW+1, labelColReserve, pillsFor(i.Labels.Nodes), right)
	}

	return ui.TwoLineRow(width, selected, glyphs, plain, styled, right, i.Title, hl)
}

// renderInboxRow draws an inbox row: unread marker, who did what, which
// issue, and the event's age.
func (i issue) renderInboxRow(width int, selected bool, hl ui.Highlighter) string {
	glyphs := ui.Dim.Render("○")
	if i.InboxUnread {
		glyphs = ui.Accent.Render("●")
	}

	plain := i.InboxEvent
	styled := ui.Yellow.Render(i.InboxEvent)
	if !i.InboxUnread {
		styled = ui.Dim.Render(i.InboxEvent)
	}
	if i.InboxActor != "" {
		plain += " · " + i.InboxActor
		styled += ui.Dim.Render(" · " + i.InboxActor)
	}
	plain += " · " + i.Identifier
	styled += "  " + ui.Cyan.Render(i.Identifier)

	right := ui.Dim.Render(ui.Age(i.UpdatedAt))
	return ui.TwoLineRow(width, selected, glyphs, plain, styled, right, i.Title, hl)
}

// --- sorting ----------------------------------------------------------------

type sortMode int

const (
	sortRecent sortMode = iota
	sortStatus
	sortProject
	sortPriority
)

var sortOrder = []sortMode{sortRecent, sortStatus, sortProject, sortPriority}
var sortName = map[sortMode]string{
	sortRecent: "date", sortStatus: "status",
	sortProject: "project", sortPriority: "priority",
}

// statusRank orders Linear's workflow state types the way a working list wants
// them: what's in flight first, then what's queued up. Unknown types sort last.
func statusRank(stateType string) int {
	switch stateType {
	case "started":
		return 0
	case "unstarted":
		return 1
	case "triage":
		return 2
	case "backlog":
		return 3
	case "completed":
		return 4
	case "canceled":
		return 5
	default:
		return 6
	}
}

// priorityRank maps Linear's priority (0 none, 1 urgent … 4 low) to an
// ascending "most important first" order, so unprioritised issues sort last.
func priorityRank(p int) int {
	if p == 0 {
		return 5
	}
	return p
}

// sortIssues returns a sorted copy of in. When rev is set the comparison is
// negated, which flips the whole ordering — primary key and tie-breaks alike —
// so "date" becomes oldest-first and "status" runs backlog-first. Equal items
// keep their original relative order either way.
func sortIssues(in []issue, mode sortMode, rev bool) []issue {
	out := make([]issue, len(in))
	copy(out, in)
	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch mode {
		case sortStatus:
			if ra, rb := statusRank(a.State.Type), statusRank(b.State.Type); ra != rb {
				return ra < rb
			}
			// Same state type but different states (e.g. two "started" columns):
			// keep them grouped by name so the list reads as columns.
			if a.State.Name != b.State.Name {
				return strings.ToLower(a.State.Name) < strings.ToLower(b.State.Name)
			}
			if ra, rb := priorityRank(a.Priority), priorityRank(b.Priority); ra != rb {
				return ra < rb
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortProject:
			// Alphabetical by project, issues without one last.
			if a.Project.Name != b.Project.Name {
				if a.Project.Name == "" || b.Project.Name == "" {
					return b.Project.Name == ""
				}
				return strings.ToLower(a.Project.Name) < strings.ToLower(b.Project.Name)
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortPriority:
			if ra, rb := priorityRank(a.Priority), priorityRank(b.Priority); ra != rb {
				return ra < rb
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		default: // recent
			return a.UpdatedAt.After(b.UpdatedAt)
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

// --- grouping ----------------------------------------------------------------

// priorityBucket names a priority swimlane, keyed by priorityRank.
var priorityBucket = map[int]string{
	1: "Urgent", 2: "High", 3: "Medium", 4: "Low", 5: "No priority",
}

// groupLabelFn returns the swimlane label for a sort mode, or nil for sorts
// with no feasible grouping. Every label follows the sort's primary key, so
// equal labels are contiguous in the sorted slice.
// sortByName resolves a configured sort name to its mode; an unknown name
// falls back to the default rather than stopping the view opening.
func sortByName(name string) (sortMode, bool) { return ui.SortByName(sortName, sortRecent, name) }

// SortNames lists the sorts this view accepts, for the settings overlay.
func SortNames() []string { return ui.SortNames(sortOrder, sortName) }

func groupLabelFn(mode sortMode) func(issue) string {
	switch mode {
	case sortRecent:
		return func(i issue) string { return ui.TimeBucket(i.UpdatedAt) }
	case sortStatus:
		return func(i issue) string { return i.State.Name }
	case sortProject:
		return func(i issue) string {
			if i.Project.Name == "" {
				return "no project"
			}
			return i.Project.Name
		}
	case sortPriority:
		return func(i issue) string { return priorityBucket[priorityRank(i.Priority)] }
	default:
		return nil
	}
}

// --- messages ---------------------------------------------------------------

// loadedMsg tags the fetched issues with their source, so a source switch
// never masquerades as "new issues" for notifications.
type loadedMsg struct {
	issues []issue
	source navSource
	// after is the cursor this page was fetched from ("" for the first),
	// cursor and hasMore say whether and where the next one starts.
	after, cursor string
	hasMore       bool
}

// pageState is where the list's paging stands for the current source:
// Linear connections carry no total, so "more" is all that is known.
type pageState struct {
	cursor  string
	hasMore bool
	loading bool
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type errMsg struct{ err error }

// --- view -------------------------------------------------------------------

type View struct {
	cfg      config.LinearConfig
	token    string
	list     ui.List[issue]
	raw      []issue
	sort     sortMode
	rev      bool // sort order reversed
	grouping bool // swimlanes derived from the active sort
	store    *store.Store

	// Navigation tree state (ctrl+p). source drives what fetch() queries;
	// defaultSource is the config-derived one whose results are cached.
	navShown bool
	navFocus bool
	// paneFocus puts the keys in the preview pane: the nav tree takes the
	// left arrow, so the pane takes the right. Both dim the list, since
	// two lit cursors say nothing about which one moves.
	paneFocus     bool
	navItems      []navItem
	navSel        int
	favs          []navProject
	favsLoaded    bool
	navErr        error
	source        navSource
	defaultSource navSource
	// page is the paging state of the list on screen; a new source resets it.
	page       pageState
	lastLoaded navSource
	// projectMine narrows a project source to your own issues ('m'). It is
	// meaningless for My Issues (already yours) and All Issues (the point
	// is everyone's), so it only applies to project sources.
	projectMine bool

	// showComments appends issue comments to the preview; fetched lazily
	// per issue and cached for the session. commentsRev bumps on every
	// fetch so the memoized preview invalidates. 'c' cycles show+jump,
	// jump, hide: commentsJumped tracks where in that cycle we are and
	// jumpPending asks the root model to scroll to commentsLine (recorded
	// while rendering the preview).
	// cfgShowComments is the configured default the toggle resets to.
	cfgShowComments bool
	// togglesPersist keeps the comments toggle when the selection moves.
	togglesPersist bool

	// fresh is the set of identifiers that arrived since the last fetch,
	// cleared per issue when you select it.
	fresh    map[string]bool
	unreadOn bool
	// jump is the jump keys' step for the tree; the list keeps its own copy.
	jump int
	// floatReveal says the detail is a float over the list rather than a
	// pane beside it; floatBase that the float was opened on the description
	// with 'v', so comments toggled over it have a level to step back to.
	floatReveal bool
	floatBase   bool
	// previewShown tracks whether the detail pane is on screen, which
	// decides what marks an issue read: hovering, or asking for the detail.
	previewShown bool

	showComments   bool
	comments       map[string]*commentsState
	commentsRev    int
	commentsJumped bool
	jumpPending    bool
	commentsLine   int

	// notifier posts "new issue" notifications (nil = off). seeded gates
	// them: only fetches after the first data (cache or live) can notify,
	// so startup never fires a storm.
	notifier notify.Notifier
	seeded   bool

	loading bool
	err     error

	listW, prevW, height int

	bodyKey string
	body    string

	keys viewKeys
}

type viewKeys struct {
	Open   key.Binding
	Copy   key.Binding
	Branch key.Binding
	Sort   key.Binding
	Rev    key.Binding
	Nav    key.Binding
	Mine   key.Binding
	Comm   key.Binding
}

func New(cfg config.LinearConfig, km config.Keymap, n notify.Notifier, st *store.Store) *View {
	bind := func(action, desc string, def ...string) key.Binding {
		return ui.Bind(km.Of("linear", action, def...), "", desc)
	}
	v := &View{
		cfg:      cfg,
		token:    cfg.Token,
		store:    st,
		notifier: n,
		list:     ui.NewList[issue](),
		loading:  cfg.Token != "",
		keys: viewKeys{
			Open:   bind("open", "open", "enter"),
			Copy:   bind("copy_url", "copy url", "y"),
			Branch: bind("copy_branch", "copy branch", "b"),
			Sort:   bind("sort", "sort", "s"),
			Rev:    bind("reverse", "reverse", "S"),
			Nav:    bind("nav", "nav pane", "ctrl+p"),
			Mine:   bind("mine", "only mine", "m"),
			Comm:   bind("comments", "comments", "c"),
		},
	}
	v.source = sourceForScope(cfg.Filter.Scope)
	v.defaultSource = v.source
	v.lastLoaded = v.source
	v.navShown = cfg.Nav
	v.showComments, v.cfgShowComments = cfg.ShowComments, cfg.ShowComments
	v.rebuildNav()
	v.list.SetRowHeight(2) // two-line rows: state/identifier + title
	v.list.Rebind(func(a string, d ...string) []string { return km.Of("list", a, d...) })
	if mode, ok := sortByName(cfg.Sort); ok {
		v.sort = mode
	}
	v.rev = cfg.Reverse

	// Paint last run's issues immediately; the live fetch refreshes them.
	if v.token != "" {
		if cached, ok := cache.Load[[]issue](cacheName); ok && len(cached) > 0 {
			v.raw = cached
			v.seeded = true
			if ids, ok := cache.Load[[]string](freshCacheName); ok && len(ids) > 0 {
				v.fresh = make(map[string]bool, len(ids))
				for _, id := range ids {
					v.fresh[id] = true
				}
			}
			v.applySort()
			v.publish(cached)
			v.loading = false
		}
	}
	return v
}

const cacheName = "linear"

// freshCacheName holds the unread set. Kept beside the issues rather than
// inside them: changing that entry's shape would throw away every existing
// cache, and an unread mark must outlive a restart to mean anything.
const freshCacheName = "linear-unread"

// freshIDs is the unread set as a sorted slice, for a stable cache file.
func (v *View) freshIDs() []string {
	if len(v.fresh) == 0 {
		return nil
	}
	out := make([]string, 0, len(v.fresh))
	for id := range v.fresh {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// saveFresh persists the unread set so marks survive a restart.
func (v *View) saveFresh() { _ = cache.Save(freshCacheName, v.freshIDs()) }

func (v *View) Title() string { return "Linear" }

// publish pushes the loaded issues into the shared store so other views (PRs,
// sessions) can show an issue's title when they reference it by identifier.
func (v *View) publish(issues []issue) {
	if v.store == nil {
		return
	}
	recs := make([]store.Issue, 0, len(issues))
	for _, i := range issues {
		recs = append(recs, store.Issue{
			Identifier: i.Identifier,
			Title:      i.Title,
			State:      i.State.Name,
			URL:        i.URL,
		})
	}
	v.store.PutIssues(recs)
}

func (v *View) Init() tea.Cmd {
	if v.token == "" {
		return nil
	}
	v.loading = true
	cmds := []tea.Cmd{v.fetch()}
	if v.navShown && !v.favsLoaded {
		v.favsLoaded = true
		cmds = append(cmds, v.fetchFavs())
	}
	return tea.Batch(cmds...)
}

// Loading includes a page in flight, so the tab's spinner runs while more
// issues are on their way rather than the list silently stalling.
func (v *View) Loading() bool { return v.loading || v.page.loading }

const issueFields = `
        identifier title url priority priorityLabel branchName updatedAt description
        state { name type color }
        team { key }
        project { name }
        assignee { displayName }
        comments(first: 50) { nodes { id } pageInfo { hasNextPage } }
        labels(first: 10) { nodes { name color } }
        attachments(first: 20) { nodes { url sourceType title metadata } }`

// assignedQuery fetches your assigned issues (the default scope); allQuery
// fetches every issue the token can see, for filter.scope: all.
const assignedQuery = `query($first: Int!, $after: String, $filter: IssueFilter) {
  viewer {
    assignedIssues(first: $first, after: $after, filter: $filter) {
      nodes {` + issueFields + `
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const allQuery = `query($first: Int!, $after: String, $filter: IssueFilter) {
  issues(first: $first, after: $after, filter: $filter, orderBy: updatedAt) {
    nodes {` + issueFields + `
    }
    pageInfo { hasNextPage endCursor }
  }
}`

// inboxQuery lists your Linear inbox: notification events with their issue.
// Other notification kinds (projects, documents) carry no issue and are
// skipped.
const inboxQuery = `query {
  notifications(first: 50) {
    nodes {
      ... on IssueNotification {
        type readAt createdAt
        actor { displayName }
        issue {` + issueFields + `
        }
      }
    }
  }
}`

// inboxEventLabel humanizes a notification type ("issueNewComment" ->
// "commented"). Unknown types fall back to the camel-case words.
func inboxEventLabel(t string) string {
	known := map[string]string{
		"issueNewComment":        "commented",
		"issueCommentMention":    "mentioned you in a comment",
		"issueCommentReaction":   "reacted to a comment",
		"issueEmojiReaction":     "reacted",
		"issueMention":           "mentioned you",
		"issueAssignedToYou":     "assigned to you",
		"issueUnassignedFromYou": "unassigned you",
		"issueStatusChanged":     "status changed",
		"issueBlocking":          "blocking your issue",
		"issueDue":               "due soon",
		"issueSubscribed":        "subscribed you",
		"issueCreated":           "created",
	}
	if l, ok := known[t]; ok {
		return l
	}
	// "issueSomethingHappened" -> "something happened"
	t = strings.TrimPrefix(t, "issue")
	var b strings.Builder
	for i, r := range t {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// buildFilter translates the config filter into Linear's IssueFilter shape.
// The zero value reproduces the historical default: not completed, not
// canceled.
func buildFilter(f config.LinearFilter) map[string]any {
	filter := map[string]any{}
	if !f.IncludeCompleted {
		filter["completedAt"] = map[string]any{"null": true}
	}
	if !f.IncludeCanceled {
		filter["canceledAt"] = map[string]any{"null": true}
	}
	if len(f.Teams) > 0 {
		filter["team"] = map[string]any{"key": map[string]any{"in": f.Teams}}
	}
	if len(f.Projects) > 0 {
		filter["project"] = map[string]any{"name": map[string]any{"in": f.Projects}}
	}
	if len(f.States) > 0 {
		filter["state"] = map[string]any{"name": map[string]any{"in": f.States}}
	}
	return filter
}

// fetch loads the first page of the current source; fetchMore the next.
func (v *View) fetch() tea.Cmd {
	v.page = pageState{}
	return v.fetchFrom("")
}

// fetchMore loads the page after the one on screen, once, when there is one.
func (v *View) fetchMore() tea.Cmd {
	if !v.page.hasMore || v.page.loading {
		return nil
	}
	v.page.loading = true
	return v.fetchFrom(v.page.cursor)
}

func (v *View) fetchFrom(after string) tea.Cmd {
	token := v.token
	first := v.cfg.Filter.Limit
	if first <= 0 {
		first = 100
	}
	filter := buildFilter(v.cfg.Filter)
	src := v.source
	if src.Kind == "project" && v.projectMine {
		src.Label = src.Label + " (mine)" // distinct tag: no notify on toggle
	}
	query := assignedQuery
	var vars map[string]any
	switch src.Kind {
	case "inbox":
		query = inboxQuery // no variables: the inbox is its own filter
	case "project":
		query = allQuery
		filter["project"] = map[string]any{"id": map[string]any{"eq": src.ProjectID}}
		if v.projectMine {
			filter["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
		}
		vars = map[string]any{"first": first, "filter": filter}
	case "all":
		query = allQuery
		vars = map[string]any{"first": first, "filter": filter}
	default:
		vars = map[string]any{"first": first, "filter": filter}
	}
	if vars != nil && after != "" {
		vars["after"] = after
	}
	return func() tea.Msg {
		payload := map[string]any{"query": query}
		if vars != nil {
			payload["variables"] = vars
		}
		body, _ := json.Marshal(payload)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return errMsg{err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token) // personal API keys: no "Bearer"

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return errMsg{err}
		}
		defer resp.Body.Close()

		var out struct {
			Data struct {
				Viewer struct {
					AssignedIssues struct {
						Nodes    []issue  `json:"nodes"`
						PageInfo pageInfo `json:"pageInfo"`
					} `json:"assignedIssues"`
				} `json:"viewer"`
				Issues struct {
					Nodes    []issue  `json:"nodes"`
					PageInfo pageInfo `json:"pageInfo"`
				} `json:"issues"`
				Notifications struct {
					Nodes []struct {
						Type      string     `json:"type"`
						ReadAt    *time.Time `json:"readAt"`
						CreatedAt time.Time  `json:"createdAt"`
						Actor     struct {
							DisplayName string `json:"displayName"`
						} `json:"actor"`
						Issue *issue `json:"issue"`
					} `json:"nodes"`
				} `json:"notifications"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return errMsg{fmt.Errorf("decoding Linear response: %w", err)}
		}
		if len(out.Errors) > 0 {
			return errMsg{fmt.Errorf("linear: %s", out.Errors[0].Message)}
		}

		nodes, pi := out.Data.Viewer.AssignedIssues.Nodes, out.Data.Viewer.AssignedIssues.PageInfo
		if len(out.Data.Issues.Nodes) > 0 {
			nodes, pi = out.Data.Issues.Nodes, out.Data.Issues.PageInfo
		}
		if src.Kind == "inbox" {
			// One row per issue, keeping its latest event (the API returns
			// notifications newest-first). The row carries the event, not
			// the bare issue: actor, action, unread state, event time.
			nodes = nodes[:0]
			seen := map[string]bool{}
			for _, n := range out.Data.Notifications.Nodes {
				if n.Issue == nil || seen[n.Issue.Identifier] {
					continue
				}
				seen[n.Issue.Identifier] = true
				it := *n.Issue
				it.InboxEvent = inboxEventLabel(n.Type)
				it.InboxActor = n.Actor.DisplayName
				it.InboxUnread = n.ReadAt == nil
				it.UpdatedAt = n.CreatedAt
				nodes = append(nodes, it)
			}
		}
		if src.Kind == "inbox" {
			pi = pageInfo{} // the inbox is one query, not paged
		}
		return loadedMsg{issues: nodes, source: src, after: after, cursor: pi.EndCursor, hasMore: pi.HasNextPage}
	}
}

// departed says why the issue you were on is gone after a refresh of the
// same source, so the cursor landing elsewhere does not pass for the same
// issue: its new state when the refresh still carried it, otherwise that
// the source no longer lists it.
func (v *View) departed(before issue, next []issue) tea.Cmd {
	if before.Identifier == "" || v.list.Any(matchID(before.Identifier)) {
		return nil
	}
	why := "no longer in this source"
	for _, i := range next {
		if i.Identifier == before.Identifier && i.State.Name != "" {
			why = "now " + i.State.Name
		}
	}
	body := before.Identifier + ": " + why
	return func() tea.Msg { return ui.ToastMsg{Title: "Left the list", Body: body} }
}

// appendIssues adds a later page, skipping issues already on screen: an
// issue updated between two page fetches can appear in both.
func appendIssues(have, more []issue) []issue {
	seen := make(map[string]bool, len(have))
	for _, i := range have {
		seen[i.Identifier] = true
	}
	for _, i := range more {
		if !seen[i.Identifier] {
			have = append(have, i)
		}
	}
	return have
}

func (v *View) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loadedMsg:
		v.loading = false
		v.err = nil
		if msg.after != "" {
			// A later page: append to what is on screen, unless the source
			// changed while it was in flight.
			v.page.loading = false
			if msg.source != v.lastLoaded {
				return nil
			}
			v.raw = appendIssues(v.raw, msg.issues)
			v.page.cursor, v.page.hasMore = msg.cursor, msg.hasMore
			v.applySort()
			v.publish(v.raw)
			if msg.source == v.defaultSource {
				_ = cache.Save(cacheName, v.raw)
			}
			return nil
		}
		v.page = pageState{cursor: msg.cursor, hasMore: msg.hasMore}
		before := v.list.Selected()
		var cmd tea.Cmd
		if msg.source == v.lastLoaded {
			v.markFresh(v.raw, msg.issues)
			cmd = v.notifyNew(v.raw, msg.issues)
		}
		sameSource := msg.source == v.lastLoaded
		v.lastLoaded = msg.source
		v.raw = msg.issues
		v.seeded = true
		v.applySort()
		v.publish(msg.issues)
		if sameSource {
			cmd = tea.Batch(cmd, v.departed(before, msg.issues))
		}
		if msg.source == v.defaultSource {
			_ = cache.Save(cacheName, msg.issues)
			v.saveFresh()
		}
		return cmd
	case issueFetchedMsg:
		if msg.err != nil || msg.issue.Identifier == "" {
			// Could not bring it in: the browser is still a way to see it.
			return ui.OpenURL(msg.url)
		}
		if !v.list.Any(matchID(msg.issue.Identifier)) {
			v.raw = append(v.raw, msg.issue)
			v.applySort()
		}
		v.list.Select(matchID(msg.issue.Identifier))
		return nil
	case favsMsg:
		v.navErr = msg.err
		v.favs = msg.projects
		v.rebuildNav()
		return nil
	case commentsMsg:
		if st, ok := v.comments[msg.id]; ok {
			st.list, st.err, st.done = msg.comments, msg.err, true
			v.commentsRev++
		}
		return nil
	case errMsg:
		if v.page.loading {
			// A later page failed: keep what is on screen rather than
			// replacing a full list with an error.
			v.page.loading = false
			return nil
		}
		v.loading = false
		v.err = msg.err
		return nil
	case ui.TogglesPersistMsg:
		v.togglesPersist = bool(msg)
		return nil
	case ui.ListJumpMsg:
		v.jump = int(msg)
		v.list.SetJump(v.jump)
		return nil
	case ui.UnreadMsg:
		// Display only: marks keep being recorded while this is off.
		v.unreadOn = bool(msg)
		v.applySort()
		return nil
	case ui.PreviewShownMsg:
		v.previewShown = bool(msg)
		v.floatFocus()
		if v.previewShown {
			v.clearFresh()
		}
		// The label column lives in the space a hidden preview frees up.
		v.applySort()
		return nil
	case ui.PreviewFloatingMsg:
		v.floatReveal = bool(msg)
		v.floatFocus()
		return nil
	case ui.GroupingMsg:
		v.grouping = bool(msg)
		v.applySort()
		return nil
	case tea.KeyMsg:
		// The nav pane toggle works regardless of focus; while the tree has
		// focus it takes the navigation keys.
		if key.Matches(msg, v.keys.Nav) {
			// ctrl+p is "take me to the tree": it shows the tree and puts the
			// keys in it, or just focuses it when the tree is already up (a
			// permanently-on tree included). Pressed with the tree focused,
			// it hides the tree.
			switch {
			case !v.navShown:
				v.navShown, v.navFocus = true, true
			case !v.navFocus:
				v.navFocus = true
			default:
				v.navShown, v.navFocus = false, false
			}
			// The tree is a different place to be: whatever pane was open
			// closes rather than three things competing for the arrows.
			var cmds []tea.Cmd
			if v.navFocus {
				if v.floatReveal {
					cmds = append(cmds, v.closeFloat())
				} else {
					v.showComments, v.commentsJumped, v.paneFocus = v.cfgShowComments, false, false
				}
			}
			v.resizeList()
			if v.navShown && !v.favsLoaded {
				v.favsLoaded = true
				cmds = append(cmds, v.fetchFavs())
			}
			return tea.Batch(cmds...)
		}
		if v.navFocus {
			return v.updateNav(msg)
		}
		// Three places can hold the keys: the tree, the list, the pane. Left
		// walks them right to left, so a focused pane hands the keys to the
		// list and only the list hands them to the tree; a float is its own
		// thing and left steps a level out of it instead.
		if !v.list.Filtering() {
			switch {
			case v.floatReveal && msg.String() == "left":
				return v.leaveFloat()
			case v.floatReveal && msg.String() == "right":
				return nil
			case v.paneFocus && msg.String() == "left":
				v.paneFocus = false
				return nil
			case !v.paneFocus && v.showComments && msg.String() == "right":
				v.paneFocus = true
				return ui.RevealPreview
			case v.navShown && msg.String() == "left":
				v.navFocus = true
				return nil
			}
		}
		before := v.list.Selected().Identifier
		if consumed, cmd := v.list.Update(msg); consumed {
			// Reaching the end is the signal to load the next page, so the
			// first paint stays one fast request.
			var more tea.Cmd
			if v.list.AtEnd() {
				more = v.fetchMore()
			}
			// Selection may have moved with the comments section showing:
			// fetch the newly-selected issue's comments if uncached, and
			// restart the 'c' jump cycle.
			v.commentsJumped = false
			if v.list.Selected().Identifier != before {
				// Moving on ends a transient preview reveal and returns the
				// comments toggle to what the config asks for. It reads the
				// issue being left, not the one arrived at, and only when
				// its detail was on screen.
				v.resetToggles()
				if v.previewShown {
					v.clearFreshFor(before)
				}
				return tea.Batch(cmd, v.maybeFetchComments(), ui.ConcealPreview, more)
			}
			return tea.Batch(cmd, v.maybeFetchComments(), more)
		}
		if v.list.Filtering() {
			return nil
		}
		switch {
		case key.Matches(msg, v.keys.Comm):
			return v.toggleComments()
		case key.Matches(msg, v.keys.Open):
			return ui.OpenURL(v.list.Selected().URL)
		case key.Matches(msg, v.keys.Copy):
			return ui.CopyCmd(v.list.Selected().URL, "URL")
		case key.Matches(msg, v.keys.Branch):
			return ui.CopyCmd(v.list.Selected().BranchName, "branch")
		case key.Matches(msg, v.keys.Mine):
			// Contextual: only a project source distinguishes "mine" from
			// "everyone's"; the fixed sources already imply it.
			if v.source.Kind != "project" {
				return nil
			}
			v.projectMine = !v.projectMine
			v.loading = true
			return v.fetch()
		case key.Matches(msg, v.keys.Sort):
			v.sort = sortOrder[(int(v.sort)+1)%len(sortOrder)]
			v.applySort()
			return nil
		case key.Matches(msg, v.keys.Rev):
			v.rev = !v.rev
			v.applySort()
			return nil
		}
	}
	return nil
}

// newIssues returns the issues in next that aren't in prev, by identifier.
func newIssues(prev, next []issue) []issue {
	known := make(map[string]bool, len(prev))
	for _, i := range prev {
		known[i.Identifier] = true
	}
	var out []issue
	for _, i := range next {
		if !known[i.Identifier] {
			out = append(out, i)
		}
	}
	return out
}

// markFresh records issues that were not in the previous set. Independent of
// notifications: the mark is how you catch up on what arrived while you were
// not looking, which is exactly when a notification gets missed.
func (v *View) markFresh(prev, next []issue) {
	// The caller only invokes this when the source is unchanged, so prev is
	// a real previous set rather than a first load.
	known := make(map[string]bool, len(prev))
	for _, i := range prev {
		known[i.Identifier] = true
	}
	for _, i := range next {
		if i.Identifier != "" && !known[i.Identifier] {
			if v.fresh == nil {
				v.fresh = map[string]bool{}
			}
			v.fresh[i.Identifier] = true
		}
	}
}

// clearFresh drops the mark for the selected issue.
func (v *View) clearFresh() { v.clearFreshFor(v.list.Selected().Identifier) }

// clearFreshFor reads one issue by identifier. The selection has already
// moved by the time a move is handled, so the caller that is leaving an
// issue has to name it: clearing "the selection" there would read the one
// you land on.
func (v *View) clearFreshFor(id string) {
	if id == "" || !v.fresh[id] {
		return
	}
	delete(v.fresh, id)
	v.applySort()
	v.saveFresh()
}

// notifyNew posts a notification for issues that appeared since the last
// fetch. nil unless the view was already seeded with data (so the first
// paint stays quiet) and a notifier is configured.
func (v *View) notifyNew(prev, next []issue) tea.Cmd {
	if v.notifier == nil || !v.seeded {
		return nil
	}
	fresh := newIssues(prev, next)
	if len(fresh) == 0 {
		return nil
	}
	title := "Linear: new issue"
	body := fresh[0].Identifier + ": " + fresh[0].Title
	if len(fresh) > 1 {
		title = fmt.Sprintf("Linear: %d new issues", len(fresh))
		body = ""
		for i, is := range fresh {
			if i > 0 {
				body += "\n"
			}
			body += is.Identifier + ": " + is.Title
		}
	}
	url := ""
	if len(fresh) == 1 {
		url = fresh[0].URL
	}
	n := v.notifier
	return func() tea.Msg { return n.Notify(title, body, url) }
}

// applySort rebuilds the list: the active sort, plus swimlane headers when
// grouping is on and this sort declares a grouping dimension.
func (v *View) applySort() {
	items := sortIssues(v.raw, v.sort, v.rev)
	// Labels fill the space a hidden preview frees up, and only when the
	// list is wide enough that they are not crowding the metadata out.
	labels := !v.previewShown && v.listW >= ui.LabelColMinRow
	for i := range items {
		items[i].Fresh = v.unreadOn && v.fresh[items[i].Identifier]
		items[i].FreshGutter = v.unreadOn
		items[i].ShowLabels = labels
	}
	if v.grouping {
		if label := groupLabelFn(v.sort); label != nil {
			items = ui.InsertGroups(items, label, func(l string) issue { return issue{Separator: l} })
			// The lane header already names the group; drop the redundant
			// per-row copy of that dimension.
			for idx := range items {
				if items[idx].Separator != "" {
					continue
				}
				switch v.sort {
				case sortStatus:
					items[idx].HideStatus = true
				case sortProject:
					items[idx].HideProject = true
				}
			}
		}
	}
	v.list.SetItems(items)
}

// ScrollList moves the list selection by n rows (mouse wheel).
func (v *View) ScrollList(n int) tea.Cmd {
	before := v.list.Selected().Identifier
	v.list.ScrollBy(n)
	return v.mouseMoved(before)
}

// ClickList handles a click in the list column; x and y are relative to the
// column's top-left. A click in the nav tree applies that source (and is not
// a row hit); a click on a row selects it and moves focus to the list.
func (v *View) ClickList(x, y int) (bool, tea.Cmd) {
	if v.token == "" {
		return false, nil
	}
	if v.navShown {
		navW := lipgloss.Width(v.navView())
		if x < navW {
			return false, v.clickNav(y)
		}
	}
	before := v.list.Selected().Identifier
	if !v.list.ClickAt(y - 1) { // line 0 is the header
		return false, nil
	}
	v.navFocus = false
	return true, v.mouseMoved(before)
}

// Activate opens the selected issue in the browser (a double-click).
func (v *View) Activate() tea.Cmd { return ui.OpenURL(v.list.Selected().URL) }

// mouseMoved follows a mouse-driven selection change the way a j/k move does:
// fetch the new issue's comments if that section is showing, restart the 'c'
// jump cycle, and end a transient preview reveal.
func (v *View) mouseMoved(before string) tea.Cmd {
	var more tea.Cmd
	if v.list.AtEnd() {
		more = v.fetchMore()
	}
	if v.list.Selected().Identifier == before {
		return more
	}
	v.resetToggles()
	return tea.Batch(v.maybeFetchComments(), ui.ConcealPreview, more)
}

func (v *View) SetSize(listW, prevW, h int) {
	was := v.listW
	v.listW, v.prevW, v.height = listW, prevW, h
	v.resizeList()
	v.bodyKey = ""
	// The label column depends on the list's width, so a resize across the
	// threshold has to re-decide it.
	if was != listW && v.seeded {
		v.applySort()
	}
}

// resizeList gives the list whatever the nav tree doesn't take. The tree
// block is measured as rendered, so border accounting can't drift.
func (v *View) resizeList() {
	w := v.listW
	if v.navShown {
		w -= lipgloss.Width(v.navView())
	}
	v.list.SetSize(max(1, w), max(1, v.height-1))
}

func (v *View) ListView() string {
	if v.token == "" {
		return ui.Faint.Render(v.setupHint())
	}
	// The list dims while the nav tree or the preview has the keys.
	v.list.SetBlurred(v.navFocus || v.paneFocus)
	header := v.list.FilterLine()
	if header == "" {
		header = ui.Faint.Render(v.statusText())
	}
	right := header + "\n" + v.list.View()
	if v.navShown {
		return lipgloss.JoinHorizontal(lipgloss.Top, v.navView(), right)
	}
	return right
}

func (v *View) setupHint() string {
	path, _ := config.Path()
	return "Linear isn't configured.\n\n" +
		"Add a personal API key to\n" + path + " :\n\n" +
		"linear:\n  token: lin_api_xxx\n\n" +
		"Create one at linear.app → Settings → Security & access → API keys."
}

func (v *View) statusText() string {
	switch {
	case v.loading:
		return "Loading Linear…"
	case v.err != nil:
		return "Error (ctrl+r to retry)"
	default:
		src := v.source.Label
		if v.source.Kind == "project" && v.projectMine {
			src += " (mine)"
		}
		count := fmt.Sprintf("%d issues", len(v.raw))
		switch {
		case v.page.loading:
			count = fmt.Sprintf("%d loaded · fetching more…", len(v.raw))
		case v.page.hasMore:
			// Linear connections carry no total, so "more below" is all
			// there is to say until the last page is in.
			count = fmt.Sprintf("%d loaded · more below", len(v.raw))
		}
		return fmt.Sprintf("%s · %s · sort: %s%s",
			count, src, sortName[v.sort], ui.RevMarker(v.rev))
	}
}

func (v *View) PreviewView() string {
	if v.token == "" {
		return ui.Faint.Width(v.prevW).Render(v.setupHint())
	}
	if v.err != nil {
		return ui.Red.Width(v.prevW).Render(v.err.Error())
	}
	i := v.list.Selected()
	if i.Identifier == "" {
		return ui.Faint.Render("No issue selected.")
	}

	var b strings.Builder
	// The same order as the list row: metadata over the title.
	b.WriteString(ui.Dim.Render(fmt.Sprintf("%s · %s ago", i.Identifier, ui.Age(i.UpdatedAt))))
	b.WriteByte('\n')
	b.WriteString(ui.Bold.Width(v.prevW).Render(i.Title))
	b.WriteString("\n\n")

	statusLine := hexStyle(i.State.Color).Render(i.State.Name)
	if i.PriorityLabel != "" && i.Priority != 0 {
		statusLine += "   " + i.priorityCell() + " " + i.PriorityLabel
	}
	if i.Project.Name != "" {
		statusLine += "   " + ui.Yellow.Render("◇ "+i.Project.Name)
	}
	b.WriteString(statusLine)
	b.WriteByte('\n')

	if pills := labelPills(i.Labels.Nodes); pills != "" {
		b.WriteString(pills)
		b.WriteByte('\n')
	}

	b.WriteString(ui.Dim.Render(strings.Repeat("─", min(v.prevW, 60))))
	b.WriteByte('\n')
	// Description, then comments, headed like the PRs view so the two
	// panes read alike.
	b.WriteString(ui.BlockHeader("Description"))
	b.WriteByte('\n')
	b.WriteString(v.renderedBody(i))
	b.WriteString("\n\n")
	b.WriteString(ui.BlockHeader("Pull requests"))
	b.WriteByte('\n')
	b.WriteString(v.renderPRs(i))
	b.WriteString("\n\n")
	// Record where the section starts so the 'c' jump can target it.
	v.commentsLine = strings.Count(b.String(), "\n") + 1
	b.WriteString(ui.BlockHeader("Comments"))
	b.WriteByte('\n')
	if v.showComments {
		b.WriteString(v.renderComments(i.Identifier, v.prevW))
	} else {
		b.WriteString(v.commentsHint(i))
	}
	return b.String()
}

// commentsMarker is the phrase the clickable comments hint ends with.
const commentsMarker = "to toggle comments"

// renderPRs is the detail's pull-request section: the PRs Linear has
// attached to the issue, one per line with the PRs view's live status
// glyphs where it has them; 'l' raises the picker to jump to one.
func (v *View) renderPRs(i issue) string {
	type row struct {
		pr               store.PR
		repo, title, url string
		num              int
	}
	var rows []row
	seen := map[string]bool{}
	for _, a := range i.Attachments.Nodes {
		repo, num, ok := ui.ParsePRURL(a.URL)
		if a.SourceType != "github" || !ok || seen[a.URL] {
			continue
		}
		seen[a.URL] = true
		pr := a.toPR()
		if v.store != nil {
			if sp, ok := v.store.PR(a.URL); ok {
				pr = sp
			}
		}
		title := pr.Title
		if title == "" {
			title = a.Title
		}
		rows = append(rows, row{pr: pr, repo: repo, num: num, title: title, url: a.URL})
	}
	if len(rows) == 0 {
		return ui.Faint.Render("  none")
	}
	var b strings.Builder
	for n, r := range rows {
		if n > 0 {
			b.WriteByte('\n')
		}
		line := "  "
		if icons := ui.PRIcons(r.pr); icons != "" {
			line += icons + "  "
		}
		line += ui.Cyan.Render(fmt.Sprintf("%s#%d", r.repo, r.num))
		if r.title != "" {
			line += "  " + ui.Truncate(r.title, max(10, v.prevW-lipgloss.Width(line)-2))
		}
		b.WriteString(line)
	}
	b.WriteString("\n" + ui.Faint.Render("  l to jump to one"))
	return b.String()
}

// issueFetchedMsg carries one issue fetched by identifier for a reference
// that was not loaded, or the failure to.
type issueFetchedMsg struct {
	id, url string
	issue   issue
	err     error
}

const issueQuery = `query($id: String!) {
  issue(id: $id) {` + issueFields + `
  }
}`

// FetchRef brings an issue that is not in the list in by identifier, so a
// reference followed from another view lands here rather than in a browser.
// Appended rather than filtered in: it may not match the current source,
// and the next refresh of that source will drop it again.
func (v *View) FetchRef(id, url string) tea.Cmd {
	token := v.token
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]any{"query": issueQuery, "variables": map[string]any{"id": id}})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return issueFetchedMsg{id: id, url: url, err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return issueFetchedMsg{id: id, url: url, err: err}
		}
		defer resp.Body.Close()
		var out struct {
			Data struct {
				Issue issue `json:"issue"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return issueFetchedMsg{id: id, url: url, err: err}
		}
		if len(out.Errors) > 0 {
			return issueFetchedMsg{id: id, url: url, err: fmt.Errorf("linear: %s", out.Errors[0].Message)}
		}
		return issueFetchedMsg{id: id, url: url, issue: out.Data.Issue}
	}
}

// commentsHint closes the summary with whether there is a conversation to
// read, and which key opens it.
func (v *View) commentsHint(i issue) string {
	n, more := i.commentCount()
	if n == 0 {
		return ui.Faint.Render("  none yet")
	}
	count := strconv.Itoa(n)
	if more {
		count += "+"
	}
	k := "c"
	if ks := v.keys.Comm.Keys(); len(ks) > 0 {
		k = ks[0]
	}
	return ui.Faint.Render(fmt.Sprintf("  %s · %s or click %s", count, k, commentsMarker))
}

// TakePreviewJump implements the root model's preview-jump hook: when a 'c'
// press requested it, scroll to the comments section. The preview renders
// first so commentsLine reflects the current selection and toggle state.
func (v *View) TakePreviewJump() (int, bool) {
	if !v.jumpPending {
		return 0, false
	}
	v.jumpPending = false
	_ = v.PreviewView()
	return v.commentsLine, true
}

func (v *View) renderedBody(i issue) string {
	desc := strings.TrimSpace(i.Description)
	if desc == "" {
		return ui.Faint.Render("(no description)")
	}
	key := fmt.Sprintf("%s:%d:%d", i.Identifier, v.prevW, ui.PaletteGen())
	if v.bodyKey == key {
		return v.body
	}
	out := ui.Markdown(desc, v.prevW)
	v.bodyKey, v.body = key, out
	return out
}

func (v *View) Bindings() []key.Binding {
	if v.token == "" {
		return nil
	}
	return []key.Binding{v.keys.Open, v.keys.Comm, v.keys.Copy, v.keys.Branch, v.keys.Sort, v.keys.Rev}
}

// Status is empty: the list header already shows the counts and sort, so the
// footer slot would only repeat them.
func (v *View) Status() string { return "" }

// PaneFocused reports whether the preview pane has the keys.
func (v *View) PaneFocused() bool { return v.paneFocus }

// FocusPane gives the pane the keys or takes them back. Beside a visible
// list only a pane with comments has anything to focus; a float takes them
// whatever it holds, since the list is behind it.
func (v *View) FocusPane(on bool) bool {
	if v.paneFocus == on || (on && !v.showComments && !v.floatReveal) {
		return false
	}
	v.paneFocus = on
	return true
}

// PaneScrolls: the comments pane scrolls rather than holding a cursor, so
// the arrows move the preview.
func (v *View) PaneScrolls() bool { return v.paneFocus }

// toggleComments is the 'c' cycle, from the key or a click on the hint:
// hidden -> show and jump to the section; visible (e.g. enabled in config)
// -> jump; already jumped -> hide, which in a float is leaving its level.
func (v *View) toggleComments() tea.Cmd {
	switch {
	case !v.showComments:
		v.showComments = true
		v.jumpPending, v.commentsJumped = true, true
		// Opening into a hidden preview is the float appearing, and a
		// float takes the keys without being asked: the list is behind it.
		// Beside a visible list, focus waits for the right arrow.
		if !v.previewShown {
			v.floatReveal = true
		}
		v.paneFocus = v.floatReveal
		return tea.Batch(ui.RevealPreview, v.maybeFetchComments())
	case !v.commentsJumped:
		v.jumpPending, v.commentsJumped = true, true
		return ui.RevealPreview
	default:
		if v.floatReveal {
			return v.leaveFloat()
		}
		v.showComments = false
		v.commentsJumped = false
		return nil
	}
}

// floatFocus settles focus from the preview's combined state: a float takes
// the keys, a side pane or nothing on screen leaves them with the list. Both
// preview messages call it because tea.Batch delivers them in either order.
func (v *View) floatFocus() {
	if v.floatReveal && v.previewShown {
		v.paneFocus = true
		if !v.showComments {
			v.floatBase = true // settled on the description: opened with 'v'
		}
		return
	}
	v.paneFocus = false
	if !v.floatReveal {
		v.floatBase = false
	}
}

// leaveFloat steps one level out of a float: comments over a 'v'
// description go back to it, anything else closes the float.
func (v *View) leaveFloat() tea.Cmd {
	if v.floatBase && v.showComments {
		v.showComments, v.commentsJumped = false, false
		v.paneFocus = true // the description scrolls, so it keeps the keys
		return nil
	}
	return v.closeFloat()
}

// closeFloat shuts a floated detail from inside, whatever level it is on.
func (v *View) closeFloat() tea.Cmd {
	v.showComments, v.commentsJumped = v.cfgShowComments, false
	v.paneFocus, v.floatReveal, v.floatBase = false, false, false
	return ui.ConcealPreview
}

// ClickPreview toggles comments when the click lands on the hint that
// names them, matched against the rendered text rather than a tracked line
// so a layout change cannot move the target.
func (v *View) ClickPreview(line, col int) tea.Cmd {
	lines := strings.Split(v.PreviewView(), "\n")
	if line < 0 || line >= len(lines) {
		return nil
	}
	text := ansi.Strip(lines[line])
	if strings.Contains(text, commentsMarker) {
		return v.toggleComments()
	}
	return nil
}

// Dismiss steps back one layer: focus to the list, then the comments pane
// shut. Reports whether it did anything, so esc can fall through to a
// floated preview when it did not.
func (v *View) Dismiss() bool {
	// Floated, esc closes the whole float whatever level it is on; the
	// arrows are what step a level at a time. Reporting nothing left to
	// do hands the close to the root model, which owns the float.
	if v.floatReveal {
		v.closeFloat()
		return false
	}
	if v.paneFocus {
		v.paneFocus = false
		return true
	}
	if v.showComments {
		v.showComments, v.commentsJumped = false, false
		return true
	}
	return false
}

func (v *View) InputActive() bool { return v.list.Filtering() }

func (v *View) Fields() []string { return v.list.FieldNames() }

func (v *View) FilterState() (string, []string, bool) {
	return v.list.Query(), v.list.EnabledFields(), v.list.CaseSensitive()
}

func (v *View) SetFilter(query string, enabled []string, caseSensitive bool) {
	v.list.SetEnabledFields(enabled)
	v.list.SetCaseSensitive(caseSensitive)
	v.list.SetQuery(query)
}

// PreviewKey folds in the comments toggle and fetch revision so the preview
// scroll resets on toggle and re-renders when comments land.
func (v *View) PreviewKey() string {
	k := v.list.Selected().Identifier
	if v.showComments {
		k += fmt.Sprintf("#comments%d", v.commentsRev)
	}
	return k
}

// RefKind / HasRef / SelectRef implement ui.RefTarget so other views can jump
// to an issue here (e.g. a PR that references it).
func (v *View) RefKind() string { return "linear" }

func matchID(id string) func(issue) bool {
	return func(i issue) bool { return strings.EqualFold(i.Identifier, id) }
}

func (v *View) HasRef(id string) bool    { return v.list.Any(matchID(id)) }
func (v *View) SelectRef(id string) bool { return v.list.Select(matchID(id)) }

// Refs implements ui.Referencer: the GitHub PRs attached to the selected issue
// (with CI/review status icons sourced from the shared store), plus the agent
// sessions that mention the issue.
func (v *View) Refs() []ui.Ref {
	sel := v.list.Selected()
	var refs []ui.Ref
	seen := map[string]bool{}
	for _, a := range sel.Attachments.Nodes {
		repo, num, ok := ui.ParsePRURL(a.URL)
		if a.SourceType != "github" || !ok || seen[a.URL] {
			continue
		}
		seen[a.URL] = true

		// Prefer the PRs view's live status (it has CI) and clean title; fall
		// back to what Linear records in the attachment metadata.
		pr := a.toPR()
		if v.store != nil {
			if sp, ok := v.store.PR(a.URL); ok {
				pr = sp
			}
		}
		title := pr.Title
		if title == "" {
			title = a.Title
		}

		refs = append(refs, ui.PRRef(pr, repo, num, title, a.URL))
	}
	if v.store != nil {
		for _, s := range v.store.SessionsMentioning(store.Key("linear", sel.Identifier)) {
			refs = append(refs, ui.SessionRef(s.Path, s.Tool, s.Cwd, s.Title, s.Snippet))
		}
	}
	return refs
}

// --- helpers ----------------------------------------------------------------

func labelPills(labels []label) string {
	if len(labels) == 0 {
		return ""
	}
	pills := make([]string, 0, len(labels))
	for _, l := range labels {
		pills = append(pills, hexStyle(l.Color).Render("● ")+l.Name)
	}
	return strings.Join(pills, "  ")
}
