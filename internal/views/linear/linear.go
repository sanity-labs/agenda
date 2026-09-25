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
	"os/exec"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	// Inbox rows represent a notification about the issue rather than the
	// issue itself: who did what (InboxEvent/InboxActor) and whether it is
	// still unread. UpdatedAt then carries the notification time.
	InboxEvent    string    `json:"-"`
	InboxActor    string    `json:"-"`
	InboxUnread   bool      `json:"-"`
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

// Selectable implements ui.NonSelectable: group headers never hold the cursor.
func (i issue) Selectable() bool { return i.Separator == "" }

func (i issue) Filter() string {
	if i.Separator != "" {
		return "\x00sep:" + i.Separator
	}
	return fmt.Sprintf("%s %s %s %s", i.Identifier, i.State.Name, i.Project.Name, i.Title)
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

func (i issue) Render(width int, selected bool, hl ui.Highlighter) string {
	if i.Separator != "" {
		return ui.GroupHeader(i.Separator, width)
	}
	if i.InboxEvent != "" {
		return i.renderInboxRow(width, selected, hl)
	}
	glyphs := i.priorityCell()

	// Metadata: state · identifier (· project), minus whatever the active
	// grouping's lane header already announces.
	plain, styled := "", ""
	if !i.HideStatus {
		plain = i.State.Name + "  "
		styled = hexStyle(i.State.Color).Render(i.State.Name) + "  "
	}
	plain += i.Identifier
	styled += ui.Cyan.Render(i.Identifier)
	if i.Project.Name != "" && !i.HideProject {
		plain += " · " + i.Project.Name
		styled += ui.Dim.Render(" · " + i.Project.Name)
	}

	right := ui.Dim.Render(ui.Age(i.UpdatedAt))

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
	navShown      bool
	navFocus      bool
	navItems      []navItem
	navSel        int
	favs          []navProject
	favsLoaded    bool
	navErr        error
	source        navSource
	defaultSource navSource
	lastLoaded    navSource
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
	v.showComments = cfg.ShowComments
	v.rebuildNav()
	v.list.SetRowHeight(2) // two-line rows: state/identifier + title
	v.list.Rebind(func(a string, d ...string) []string { return km.Of("list", a, d...) })

	// Paint last run's issues immediately; the live fetch refreshes them.
	if v.token != "" {
		if cached, ok := cache.Load[[]issue](cacheName); ok && len(cached) > 0 {
			v.raw = cached
			v.seeded = true
			v.applySort()
			v.publish(cached)
			v.loading = false
		}
	}
	return v
}

const cacheName = "linear"

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

func (v *View) Loading() bool { return v.loading }

const issueFields = `
        identifier title url priority priorityLabel branchName updatedAt description
        state { name type color }
        team { key }
        project { name }
        assignee { displayName }
        labels(first: 10) { nodes { name color } }
        attachments(first: 20) { nodes { url sourceType title metadata } }`

// assignedQuery fetches your assigned issues (the default scope); allQuery
// fetches every issue the token can see, for filter.scope: all.
const assignedQuery = `query($first: Int!, $filter: IssueFilter) {
  viewer {
    assignedIssues(first: $first, filter: $filter) {
      nodes {` + issueFields + `
      }
    }
  }
}`

const allQuery = `query($first: Int!, $filter: IssueFilter) {
  issues(first: $first, filter: $filter, orderBy: updatedAt) {
    nodes {` + issueFields + `
    }
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

func (v *View) fetch() tea.Cmd {
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
						Nodes []issue `json:"nodes"`
					} `json:"assignedIssues"`
				} `json:"viewer"`
				Issues struct {
					Nodes []issue `json:"nodes"`
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

		nodes := out.Data.Viewer.AssignedIssues.Nodes
		if len(out.Data.Issues.Nodes) > 0 {
			nodes = out.Data.Issues.Nodes
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
		return loadedMsg{issues: nodes, source: src}
	}
}

func (v *View) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loadedMsg:
		v.loading = false
		v.err = nil
		var cmd tea.Cmd
		if msg.source == v.lastLoaded {
			cmd = v.notifyNew(v.raw, msg.issues)
		}
		v.lastLoaded = msg.source
		v.raw = msg.issues
		v.seeded = true
		v.applySort()
		v.publish(msg.issues)
		if msg.source == v.defaultSource {
			_ = cache.Save(cacheName, msg.issues)
		}
		return cmd
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
		v.loading = false
		v.err = msg.err
		return nil
	case ui.GroupingMsg:
		v.grouping = bool(msg)
		v.applySort()
		return nil
	case tea.KeyMsg:
		// The nav pane toggle works regardless of focus; while the tree has
		// focus it takes the navigation keys.
		if key.Matches(msg, v.keys.Nav) {
			v.navShown = !v.navShown
			if !v.navShown {
				v.navFocus = false
			}
			v.resizeList()
			if v.navShown && !v.favsLoaded {
				v.favsLoaded = true
				return v.fetchFavs()
			}
			return nil
		}
		if v.navFocus {
			return v.updateNav(msg)
		}
		if v.navShown && msg.String() == "left" && !v.list.Filtering() {
			v.navFocus = true
			return nil
		}
		before := v.list.Selected().Identifier
		if consumed, cmd := v.list.Update(msg); consumed {
			// Selection may have moved with the comments section showing:
			// fetch the newly-selected issue's comments if uncached, and
			// restart the 'c' jump cycle.
			v.commentsJumped = false
			if v.list.Selected().Identifier != before {
				// Moving on ends a transient preview reveal.
				return tea.Batch(cmd, v.maybeFetchComments(), ui.ConcealPreview)
			}
			return tea.Batch(cmd, v.maybeFetchComments())
		}
		if v.list.Filtering() {
			return nil
		}
		switch {
		case key.Matches(msg, v.keys.Comm):
			// Cycle: hidden -> show and jump to the section; visible (e.g.
			// enabled in config) -> jump; already jumped -> hide.
			switch {
			case !v.showComments:
				v.showComments = true
				v.jumpPending, v.commentsJumped = true, true
				return tea.Batch(ui.RevealPreview, v.maybeFetchComments())
			case !v.commentsJumped:
				v.jumpPending, v.commentsJumped = true, true
				return ui.RevealPreview
			default:
				v.showComments = false
				v.commentsJumped = false
				return nil
			}
		case key.Matches(msg, v.keys.Open):
			return ui.OpenURL(v.list.Selected().URL)
		case key.Matches(msg, v.keys.Copy):
			return copyCmd(v.list.Selected().URL)
		case key.Matches(msg, v.keys.Branch):
			return copyCmd(v.list.Selected().BranchName)
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
	n := v.notifier
	return func() tea.Msg { return n.Notify(title, body) }
}

// applySort rebuilds the list: the active sort, plus swimlane headers when
// grouping is on and this sort declares a grouping dimension.
func (v *View) applySort() {
	items := sortIssues(v.raw, v.sort, v.rev)
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
	if v.list.Selected().Identifier == before {
		return nil
	}
	v.commentsJumped = false
	return tea.Batch(v.maybeFetchComments(), ui.ConcealPreview)
}

func (v *View) SetSize(listW, prevW, h int) {
	v.listW, v.prevW, v.height = listW, prevW, h
	v.resizeList()
	v.bodyKey = ""
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
		return fmt.Sprintf("%d issues · %s · sort: %s%s",
			len(v.raw), src, sortName[v.sort], ui.RevMarker(v.rev))
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
	b.WriteString(ui.Bold.Width(v.prevW).Render(i.Title))
	b.WriteByte('\n')
	b.WriteString(ui.Dim.Render(fmt.Sprintf("%s · %s ago", i.Identifier, ui.Age(i.UpdatedAt))))
	b.WriteByte('\n')

	statusLine := hexStyle(i.State.Color).Render(i.State.Name)
	if i.PriorityLabel != "" && i.Priority != 0 {
		statusLine += "   " + i.priorityCell() + " " + i.PriorityLabel
	}
	if i.Project.Name != "" {
		statusLine += "   " + ui.Dim.Render("◇ "+i.Project.Name)
	}
	b.WriteString(statusLine)
	b.WriteByte('\n')

	if i.BranchName != "" {
		b.WriteString(ui.Dim.Render(" " + i.BranchName))
		b.WriteByte('\n')
	}
	if pills := labelPills(i.Labels.Nodes); pills != "" {
		b.WriteString(pills)
		b.WriteByte('\n')
	}

	b.WriteString(ui.Dim.Render(strings.Repeat("─", min(v.prevW, 60))))
	b.WriteByte('\n')
	b.WriteString(v.renderedBody(i))
	if v.showComments {
		b.WriteByte('\n')
		// Record where the section starts so the 'c' jump can target it.
		v.commentsLine = strings.Count(b.String(), "\n") + 1
		b.WriteString(v.renderComments(i.Identifier, v.prevW))
	}
	return b.String()
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

func (v *View) Status() string { return ui.Dim.Render(v.statusText()) }

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

func copyCmd(s string) tea.Cmd {
	if s == "" {
		return nil
	}
	return func() tea.Msg {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(s)
		_ = c.Run()
		return nil
	}
}
