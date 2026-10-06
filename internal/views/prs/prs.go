// Package prs is agenda's GitHub pull-requests view. It lists the PRs matching
// a configurable search query and previews the selected one.
//
// Data comes from the GitHub GraphQL API via `gh api graphql`, which reuses
// the user's existing gh auth and — unlike `gh search prs --json` — exposes
// the rich fields that make the view useful: CI check rollup, review decision,
// diff size, comment count, mergeability, and colored labels. This mirrors the
// approach gh-dash takes.
package prs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os/exec"
	"regexp"
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

// --- data -------------------------------------------------------------------

type label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// pr is one row, decoded from the GraphQL search result. A row with a
// non-empty Separator is a divider, not a pull request: the mine/review
// section split, or (with Group set) a swimlane header.
type pr struct {
	Separator      string    `json:"-"`
	Group          bool      `json:"-"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"isDraft"`
	UpdatedAt      time.Time `json:"updatedAt"`
	HeadRefName    string    `json:"headRefName"`
	BaseRefName    string    `json:"baseRefName"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	Mergeable      string    `json:"mergeable"`
	ReviewDecision string    `json:"reviewDecision"`
	Body           string    `json:"body"`
	Author         struct {
		Login string `json:"login"`
	} `json:"author"`
	ViewerLatestReview struct {
		State string `json:"state"`
	} `json:"viewerLatestReview"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Login        string `json:"login"`
				CombinedSlug string `json:"combinedSlug"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	// Unread marks a row that arrived since the last fetch, cleared when you
	// select it, so a notification you missed is still visible in the list.
	// UnreadGutter reserves the column even when this row is read, so marks
	// clearing does not shift every other row.
	Unread       bool `json:"-"`
	UnreadGutter bool `json:"-"`
	// Reviewed marks a review-requested row the viewer has already reviewed
	// (set at assembly when github.mark_reviewed is on): rendered dim with a
	// "reviewed" tag so the eye can skip it.
	Reviewed bool `json:"-"`
	// ShowLabels adds a label column, set at assembly when the preview pane
	// is off and the row is wide enough to spare the space.
	ShowLabels bool `json:"-"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	Labels struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Commits struct {
		Nodes []commitNode `json:"nodes"`
	} `json:"commits"`
}

// commitNode is the last commit of a PR, carrying its check rollup.
type commitNode struct {
	Commit struct {
		StatusCheckRollup struct {
			State string `json:"state"`
			// Aggregate counts rather than the check nodes: the same
			// "8 successful" gh-dash shows, for no extra query cost.
			Contexts struct {
				TotalCount                 int            `json:"totalCount"`
				CheckRunCount              int            `json:"checkRunCount"`
				CheckRunCountsByState      []contextCount `json:"checkRunCountsByState"`
				StatusContextCount         int            `json:"statusContextCount"`
				StatusContextCountsByState []contextCount `json:"statusContextCountsByState"`
			} `json:"contexts"`
		} `json:"statusCheckRollup"`
	} `json:"commit"`
}

// contextCount is one bucket of the check rollup: how many checks are in a
// given state.
type contextCount struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

func (p pr) repo() string { return p.Repository.NameWithOwner }

// checkCounts sums the rollup buckets into what the preview reports:
// how many checks passed, failed, and are still running.
func (p pr) checkCounts() (passed, failed, running, total int) {
	if len(p.Commits.Nodes) == 0 {
		return 0, 0, 0, 0
	}
	cx := p.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts
	for _, b := range append(append([]contextCount{}, cx.CheckRunCountsByState...), cx.StatusContextCountsByState...) {
		switch b.State {
		case "SUCCESS", "COMPLETED", "NEUTRAL", "SKIPPED":
			passed += b.Count
		case "FAILURE", "ERROR", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "CANCELLED":
			failed += b.Count
		case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "EXPECTED", "STALE":
			running += b.Count
		}
	}
	return passed, failed, running, cx.TotalCount
}

// reviewedByMe reports whether the viewer's latest review still counts as
// "handled": approved, changes requested, or commented. DISMISSED and PENDING
// mean the ball is back with the viewer.
// approvedAndOpen reports a PR that is approved and still open: the case
// hide_approved is about. reviewDecision is the PR's overall decision, so
// an approval by anyone counts, not just the viewer's own.
func (p pr) approvedAndOpen() bool {
	return p.ReviewDecision == "APPROVED" && p.State == "OPEN"
}

func (p pr) reviewedByMe() bool {
	switch p.ViewerLatestReview.State {
	case "APPROVED", "CHANGES_REQUESTED", "COMMENTED":
		return true
	}
	return false
}

func (p pr) ciState() string {
	if len(p.Commits.Nodes) == 0 {
		return ""
	}
	return p.Commits.Nodes[0].Commit.StatusCheckRollup.State
}

// Selectable implements ui.NonSelectable: separators never hold the cursor.
func (p pr) Selectable() bool { return p.Separator == "" }

func (p pr) Filter() string {
	if p.Separator != "" {
		return "\x00sep:" + p.Separator
	}
	return fmt.Sprintf("%s #%d %s", p.repo(), p.Number, p.Title)
}

// Key is the row's identity across refreshes: the URL, which a title edit
// or a review does not change.
func (p pr) Key() string {
	if p.Separator != "" {
		return "\x00sep:" + p.Separator
	}
	return p.URL
}

func (p pr) Fields() []ui.Field {
	if p.Separator != "" {
		return nil
	}
	return []ui.Field{
		{Name: "repo", Text: p.repo()},
		{Name: "branch", Text: p.HeadRefName},
		{Name: "title", Text: p.Title},
		{Name: "description", Text: p.Body},
		{Name: "author", Text: p.Author.Login},
		// Qualifier-only, so the in-app filter can say "-label:deps" or
		// "is:draft" the way a GitHub search does. Qualified keeps them out
		// of the bare-word match: searching "open" should not return every
		// open PR.
		{Name: "label", Text: p.labelText(), Qualified: true},
		{Name: "is", Text: p.stateTerms(), Qualified: true},
		{Name: "review", Text: p.reviewTerm(), Qualified: true},
		{Name: "checks", Text: p.checksTerm(), Qualified: true},
	}
}

// labelText joins the PR's label names, for the "label:" qualifier.
func (p pr) labelText() string {
	names := make([]string, 0, len(p.Labels.Nodes))
	for _, l := range p.Labels.Nodes {
		names = append(names, l.Name)
	}
	return strings.Join(names, " ")
}

// stateTerms are the words "is:" accepts for this PR. Several can apply at
// once (an open draft), so the field holds them all.
func (p pr) stateTerms() string {
	var terms []string
	switch strings.ToUpper(p.State) {
	case "OPEN":
		terms = append(terms, "open")
	case "MERGED":
		terms = append(terms, "merged")
	case "CLOSED":
		terms = append(terms, "closed")
	}
	if p.IsDraft {
		terms = append(terms, "draft")
	}
	if p.Mergeable == "CONFLICTING" {
		terms = append(terms, "conflicting")
	}
	return strings.Join(terms, " ")
}

// reviewTerm is the word "review:" accepts, mirroring GitHub's own
// review:approved / review:required / review:changes_requested.
func (p pr) reviewTerm() string {
	switch p.ReviewDecision {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "REVIEW_REQUIRED":
		return "required"
	}
	return "none"
}

// checksTerm is the word "checks:" accepts: passing, failing or pending.
func (p pr) checksTerm() string {
	pass, fail, run, total := p.checkCounts()
	switch {
	case total == 0 && p.ciState() == "":
		return "none"
	case fail > 0:
		return "failing"
	case run > 0:
		return "pending"
	case pass > 0:
		return "passing"
	}
	return "none"
}

// linearRefRe matches a Linear issue identifier (team key + number), e.g.
// "SRE-4228" in a title or "sre-3686" in a branch name like
// "orjan/sre-3686-add-foo". The team key is letters only, so version-ish
// tokens like "v2-foo" don't match.
var linearRefRe = regexp.MustCompile(`(?i)\b([a-z]{2,}-\d+)\b`)

// linearRefs returns the Linear identifiers this PR references (uppercased,
// de-duplicated, in order of appearance). It scans the title, branch, then
// body — the places a Linear issue is conventionally named.
func (p pr) linearRefs() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range []string{p.Title, p.HeadRefName, p.Body} {
		for _, m := range linearRefRe.FindAllStringSubmatch(s, -1) {
			id := strings.ToUpper(m[1])
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// --- icon rendering ---------------------------------------------------------

func (p pr) stateIcon() string {
	switch {
	case p.IsDraft:
		return ui.Dim.Render(ui.IconDraft)
	case p.State == "MERGED":
		return ui.Magenta.Render(ui.IconMerged)
	case p.State == "CLOSED":
		return ui.Red.Render(ui.IconClosed)
	default:
		return ui.Green.Render(ui.IconOpen)
	}
}

func (p pr) ciIcon() string {
	switch p.ciState() {
	case "SUCCESS":
		return ui.Green.Render(ui.IconCIOK)
	case "FAILURE", "ERROR":
		return ui.Red.Render(ui.IconCIFail)
	case "PENDING", "EXPECTED":
		return ui.Yellow.Render(ui.IconCIPending)
	default:
		return ui.Dim.Render(ui.IconDot)
	}
}

func (p pr) reviewIcon() string {
	switch p.ReviewDecision {
	case "APPROVED":
		return ui.Green.Render(ui.IconApproved)
	case "CHANGES_REQUESTED":
		return ui.Red.Render(ui.IconChanges)
	case "REVIEW_REQUIRED":
		return ui.Yellow.Render(ui.IconReviewReq)
	default:
		return ui.Dim.Render(ui.IconDot)
	}
}

func (p pr) diffCell() string {
	if p.Additions == 0 && p.Deletions == 0 {
		return ""
	}
	return ui.Green.Render("+"+compactCount(p.Additions)) + " " +
		ui.Red.Render("-"+compactCount(p.Deletions))
}

// diffPlain / commentsPlain are the uncolored cell texts, for dim rows.
func (p pr) commentsCell() string {
	if p.Comments.TotalCount == 0 {
		return ""
	}
	return ui.Dim.Render(p.commentsText())
}

// Render draws one PR as a two-line block, à la gh-dash's non-compact layout:
//
//	▌  ● ● ●  repo #123 · @author · branch          +12 -3  3  2d
//	          The pull request title, in bold
//
// Line one is dimmed metadata with the status glyphs; line two is the title,
// indented to align under the metadata. The selected row gets an accent bar on
// both lines (rather than a full-row background, which lipgloss's per-segment
// resets would clobber).
func (p pr) Render(width int, selected bool, hl ui.Highlighter) string {
	if p.Separator != "" {
		if p.Group {
			return ui.GroupHeader(p.Separator, width)
		}
		return ui.SectionSeparator(p.Separator, width)
	}
	glyphs := p.stateIcon() + " " + p.ciIcon() + " " + p.reviewIcon()
	// An unread row leads with a bold blue dot: blue is the one palette
	// colour distinct from the accent in every built-in theme, and it is
	// not already spoken for by a status glyph. The gutter is always there,
	// blank once read, so clearing a mark does not shift the row sideways.
	if p.UnreadGutter {
		mark := strings.Repeat(" ", lipgloss.Width(ui.IconUnread))
		if p.Unread {
			mark = ui.Blue.Bold(true).Render(ui.IconUnread)
		}
		glyphs = mark + " " + glyphs
	}

	// Right cluster: labels · diff · comments · age. The numeric cells are
	// padded to a fixed width so they form columns instead of drifting with
	// their contents; labels take whatever is left after them.
	right := p.rightCluster(width, false)

	// Metadata: repo #num · @author · branch (plain for measurement/truncation,
	// styled for display).
	plain := fmt.Sprintf("%s #%d", p.repo(), p.Number)
	styled := ui.Cyan.Render(p.repo()) + ui.Yellow.Render(fmt.Sprintf(" #%d", p.Number))
	if p.Author.Login != "" {
		plain += " · @" + p.Author.Login
		styled += ui.Dim.Render(" · @" + p.Author.Login)
	}
	if p.HeadRefName != "" {
		plain += " · " + p.HeadRefName
		styled += ui.Dim.Render(" · " + p.HeadRefName)
	}

	// Already reviewed by you: tag the metadata and dim the whole row so the
	// eye skims past it (glyphs keep their colors; status stays readable).
	if p.Reviewed {
		plain += " · reviewed"
		styled = ui.Dim.Render(plain)
		right = p.rightCluster(width, true)
		return ui.TwoLineRowFaint(width, selected, glyphs, plain, styled, right, p.Title, hl)
	}

	return ui.TwoLineRow(width, selected, glyphs, plain, styled, right, p.Title, hl)
}

// --- sorting ----------------------------------------------------------------

type sortMode int

const (
	sortRecent sortMode = iota
	sortReview
	sortChecks
	sortRepo
	sortSize
	sortAuthor
)

var sortOrder = []sortMode{sortRecent, sortReview, sortChecks, sortRepo, sortSize, sortAuthor}
var sortName = map[sortMode]string{
	sortRecent: "date", sortReview: "review", sortChecks: "checks",
	sortRepo: "repo", sortSize: "size", sortAuthor: "author",
}

// groupLabelFn returns the swimlane label for a sort mode (nil = flat). The
// label follows each sort's primary key, so equal labels are contiguous.
// sortByName resolves a configured sort name to its mode. An unknown name
// falls back to the default rather than failing: a typo in the config
// should not stop the view opening.
func sortByName(name string) (sortMode, bool) { return ui.SortByName(sortName, sortRecent, name) }

// SortNames lists the sorts this view accepts, for the settings overlay.
func SortNames() []string { return ui.SortNames(sortOrder, sortName) }

func groupLabelFn(mode sortMode) func(pr) string {
	switch mode {
	case sortRecent:
		return func(p pr) string { return ui.TimeBucket(p.UpdatedAt) }
	case sortReview:
		return func(p pr) string { return reviewBucket[reviewRank(p)] }
	case sortChecks:
		return func(p pr) string { return checksBucket[checksRank(p)] }
	case sortRepo:
		return func(p pr) string { return p.repo() }
	case sortSize:
		return func(p pr) string { return sizeBucket(p.size()) }
	case sortAuthor:
		return func(p pr) string { return "@" + p.Author.Login }
	default:
		return nil
	}
}

// reviewBucket and checksBucket name the swimlanes for the review and checks
// sorts, keyed by their rank functions.
var reviewBucket = map[int]string{
	0: "Changes requested", 1: "Review required", 2: "Unreviewed", 3: "Approved",
}

var checksBucket = map[int]string{
	0: "Checks failing", 1: "Checks running", 2: "No checks", 3: "Checks passing",
}

// sizeBucket names the swimlane for a PR's total churn, using the usual
// T-shirt thresholds.
func sizeBucket(churn int) string {
	switch {
	case churn < 10:
		return "XS"
	case churn < 50:
		return "S"
	case churn < 250:
		return "M"
	case churn < 1000:
		return "L"
	default:
		return "XL"
	}
}

// reviewRank orders PRs by how much review attention they need: whatever is
// blocked or unseen first, approved last.
func reviewRank(p pr) int {
	switch p.ReviewDecision {
	case "CHANGES_REQUESTED":
		return 0
	case "REVIEW_REQUIRED":
		return 1
	case "APPROVED":
		return 3
	default: // no review decision yet
		return 2
	}
}

// checksRank orders PRs worst-CI-first, so anything red or unfinished floats
// above the green ones.
func checksRank(p pr) int {
	switch p.ciState() {
	case "FAILURE", "ERROR":
		return 0
	case "PENDING", "EXPECTED":
		return 1
	case "SUCCESS":
		return 3
	default: // no checks configured or reported
		return 2
	}
}

// size is the PR's total churn, used by the size sort.
func (p pr) size() int { return p.Additions + p.Deletions }

// sortPRs returns a sorted copy of in. When rev is set the comparison is
// negated, which flips the whole ordering — primary key and tie-breaks alike —
// so "date" becomes oldest-first, "size" biggest-first, and so on. Equal items
// keep their original relative order either way.
func sortPRs(in []pr, mode sortMode, rev bool) []pr {
	out := make([]pr, len(in))
	copy(out, in)
	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch mode {
		case sortReview:
			if ra, rb := reviewRank(a), reviewRank(b); ra != rb {
				return ra < rb
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortChecks:
			if ra, rb := checksRank(a), checksRank(b); ra != rb {
				return ra < rb
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortRepo:
			if a.repo() != b.repo() {
				return strings.ToLower(a.repo()) < strings.ToLower(b.repo())
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortSize:
			if a.size() != b.size() {
				return a.size() < b.size() // smallest diff first: quickest to review
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortAuthor:
			if a.Author.Login != b.Author.Login {
				return strings.ToLower(a.Author.Login) < strings.ToLower(b.Author.Login)
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

// --- messages ---------------------------------------------------------------

// The two searches deliver independently: the user's own PRs paint the list
// as soon as they land (~seconds), while the review-requested search, which
// can match ~100 PRs across an org and take ~10s or hit GitHub's gateway
// timeout, streams into its section later and fails on its own without
// taking the tab down.
type mineMsg struct {
	// from is the view whose search this answers. The root model
	// broadcasts data messages to every view, and the PRs and Reviews tabs
	// are the same type, so each would otherwise take the other's rows.
	// nil (tests) means mine.
	from *View
	page searchPage
	err  error
	// more marks a page fetched after the first, appended rather than
	// replacing what is already on screen.
	more bool
}

type reviewListMsg struct {
	from *View
	page searchPage
	err  error
	more bool
}

// --- view -------------------------------------------------------------------

type View struct {
	cfg        config.GitHubConfig
	list       ui.List[pr]
	raw        []pr // own PRs
	reviewRaw  []pr // PRs waiting on the user's review
	showReview bool // render the review section (toggled with 'w')
	// reviewsOnly makes this the Reviews tab: the review-requested search is
	// the whole list, and the own-PR search never runs. reviewsElsewhere is
	// the PRs tab beside one: its review section is off and stays off, since
	// that tab owns the search, the cache and the unread marks.
	reviewsOnly      bool
	reviewsElsewhere bool
	grouping         bool // swimlanes derived from the active sort
	sort             sortMode
	rev              bool // sort order reversed
	store            *store.Store

	// notifier posts "needs your review" notifications (nil = off); seeded
	// gates them so the first data never fires a storm.
	notifier notify.Notifier
	seeded   bool

	loading bool
	err     error

	// The review search loads and fails independently of the main list.
	reviewLoading bool
	reviewErr     error

	listW, prevW, height int

	// memoized glamour render of the selected PR's body, keyed by number+width
	// so it isn't re-rendered every frame.
	bodyKey string
	// expanded is the PR whose summary the user expanded with 'e'; cleared
	// when the selection moves, so expansion does not leak between rows.
	expanded string
	// togglesPersist keeps per-item toggles (the pane, the expansion) when
	// the selection moves. Off by default: a diff opened on one PR should
	// not put every other PR in diff view.
	togglesPersist bool
	body           string

	// pane picks what the right pane shows for the selection: description,
	// diff ('d'), or comments ('c'). diffs and comments cache fetched data
	// by PR URL for the session.
	pane     paneMode
	diffs    map[string]diffState
	comments map[string]*commentsState

	// Paging: where each section's next page starts, and whether one is in
	// flight. An empty cursor with hasMore false means fully loaded.
	minePage, reviewPage pageState

	// previewShown tracks whether the detail pane is on screen, which
	// decides what marks a row read: hovering, or asking for the detail.
	previewShown bool
	// jump is the jump keys' step for the panes that hold their own cursor
	// (jobs, files, the log); the list keeps its own copy.
	jump int
	// vpOffset and vpRows are the preview lines on screen as of the last
	// render, from the root model, so the file list can tell whether the
	// next file is visible before moving to it.
	vpOffset, vpRows int
	// floatBase records that the float was opened on the description with
	// 'v', so a pane toggled over it has a level to step back to.
	floatBase bool
	// floatReveal distinguishes a float, which closes when you move on,
	// from a pane that stays open and shows the row you arrive at.
	floatReveal bool

	// rowRefresh re-reads the selected PR once the cursor settles; rowGen
	// supersedes earlier ticks so cycling a list costs one request.
	rowRefresh bool
	rowGen     int

	// hideApproved drops already-approved PRs from the review list.
	hideApproved bool
	// hideDrafts drops draft PRs from both sections.
	hideDrafts bool

	// filterEd is the open search-filter editor ('F'), nil when closed.
	filterEd *filterEdit

	// unread is the set of URLs that arrived since the last fetch, by URL so
	// it survives re-sorts and re-fetches. Selecting a row removes it.
	unread   map[string]bool
	unreadOn bool
	// unreadSync mirrors a read back to the GitHub notification; off by
	// default, since moving the cursor should not clear your real inbox.
	unreadSync bool
	// syncPending collects write-backs earned by clearUnread, drained by
	// the caller that has a tea.Cmd to return.
	syncPending []tea.Cmd
	// mineSeeded marks the own-PRs list as having a baseline to diff
	// against; seeded is the review list's equivalent.
	mineSeeded bool

	// settleGen drops stale selection-settle ticks, so only the final
	// position of a navigation burst triggers pane fetches.
	settleGen int

	// anchors are the jump targets in the current pane render (inline
	// threads); annIdx is the current one, and pendingJump carries a
	// requested preview scroll the root model picks up. commentsRev bumps
	// on every comments fetch so memoized panes invalidate; paneKey/
	// paneText/paneAnchors memoize the last rendered data pane.
	anchors     []ui.DiffAnchor
	annIdx      int
	pendingJump *int
	commentsRev int
	paneKey     string
	paneText    string
	paneAnchors []ui.DiffAnchor
	paneHeader  int

	// review is the in-flight review flow ('r'), nil when inactive; input
	// is an open reply/comment prompt. flash is a transient status-line
	// result (cleared on the next fetch).
	review *reviewFlow
	// spinFrame follows the root spinner so the popup's glyph turns with it.
	spinFrame int
	input     *threadFlow
	flash     string

	// jobs caches each PR's check runs for the jobs pane ('t'), by URL;
	// jobsGen supersedes pending watch ticks. rerun is the open rerun
	// popup ('x'), nil when inactive.
	jobs    map[string]*jobsState
	jobsGen int
	rerun   *rerunFlow
	// jobsFocus puts the keys in the jobs pane rather than the PR list.
	// jobSel and jobsOpen are its cursor and expanded jobs, belonging to
	// the PR jobsFor; nav is the list's movement keys, reused there.
	jobsFocus bool
	// files is the per-PR file list ('D'), keyed by URL like the diffs.
	files map[string]*filesState
	// paneFocus is focus for the panes that scroll (a diff, comments)
	// rather than holding their own cursor. jobsFocus is the jobs pane's
	// own, since it tracks a row as well.
	paneFocus bool
	jobSel    jobCursor
	jobsOpen  map[string]bool
	jobsFor   string
	nav       navKeys
	// logs caches job logs by job id; logView is the one open in the pane
	// (enter on a step), and scrollBy a relative scroll it asks the root
	// for.
	logs     map[int64]*logState
	logView  *logView
	scrollBy int

	keys viewKeys
}

// settleMsg fires after navigation pauses; only the newest generation acts.
type settleMsg struct {
	from *View
	gen  int
}

// scheduleSettle arms the debounce while a data pane is showing.
func (v *View) scheduleSettle() tea.Cmd {
	if v.pane == paneBody {
		return nil
	}
	v.settleGen++
	gen := v.settleGen
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return settleMsg{from: v, gen: gen} })
}

// paneMode selects the right pane's content for the selected PR.
type paneMode int

const (
	paneBody paneMode = iota
	paneDiff
	paneComments
	paneJobs
	paneFiles
)

// reviewFlow drives the review popup: pick an option, then (for comment /
// request-changes) type a body, then submit via gh.
type reviewFlow struct {
	url, repo  string
	num        int
	sel        int    // cursor over the options while picking
	verdict    string // "", then "approve" | "comment" | "request-changes"
	body       string
	submitting bool
	// action is what gh is running, or ran: "review", "merge" or "auto".
	// done and fail hold the outcome, and the popup stays up to show it.
	action, done, fail string
	// confirm names the merge awaiting a yes ("merge" or "auto"), and why
	// it is a bad idea when it is: merging cannot be undone by another
	// keypress, so it never happens on the first one.
	confirm, warn string
	// blocked explains why a merge is not offered at all (a draft, a
	// conflict, changes requested), so the popup says so rather than
	// failing after the fact.
	blocked string
}

// reviewOption is one popup entry: a hotkey, a label, and the gh verdict
// ("" for entries that are not a review submission).
type reviewOption struct {
	key, label, verdict string
}

// reviewOptions are the popup's entries. The review verdicts are always
// there; the merge entries appear only when github.merge is on, since
// merging is irreversible and not everyone wants it a keypress away.
var reviewOptions = []reviewOption{
	{"a", "Approve", "approve"},
	{"c", "Comment", "comment"},
	{"x", "Request changes", "request-changes"},
	{"d", "View diff", ""},
	{"", "Cancel", ""},
}

// mergeLabel and autoLabel are the merge entries' labels, kept as
// constants because the flow matches on them.
const (
	mergeLabel = "Merge"
	autoLabel  = "Enable auto-merge"
)

// options are the popup's entries for this view: the review verdicts, plus
// the merge entries when they are configured on.
func (v *View) options() []reviewOption {
	if !v.cfg.Merge {
		return reviewOptions
	}
	opts := make([]reviewOption, 0, len(reviewOptions)+2)
	// Merge sits after the verdicts and before the read-only entries, so a
	// fat-fingered 'd' or Cancel cannot land on it.
	opts = append(opts, reviewOptions[:3]...)
	opts = append(opts,
		reviewOption{"m", mergeLabel, ""},
		reviewOption{"M", autoLabel, ""},
	)
	return append(opts, reviewOptions[3:]...)
}

type viewKeys struct {
	Open       key.Binding
	Copy       key.Binding
	Diff       key.Binding
	Sort       key.Binding
	Rev        key.Binding
	Review     key.Binding
	HideDrafts key.Binding
	Start      key.Binding
	Comments   key.Binding
	NextThread key.Binding
	PrevThread key.Binding
	Reply      key.Binding
	Resolve    key.Binding
	TopComment key.Binding
	Expand     key.Binding
	Jobs       key.Binding
	OpenJob    key.Binding
	JobLog     key.Binding
	Rerun      key.Binding
	EditFilter key.Binding
}

// binding looks a binding up by its action name, for prompts that name the
// key rather than hard-coding it (the keys are remappable).
func (k viewKeys) binding(action string) key.Binding {
	switch action {
	case "expand":
		return k.Expand
	case "comments":
		return k.Comments
	case "jobs":
		return k.Jobs
	case "next_thread":
		return k.NextThread
	case "prev_thread":
		return k.PrevThread
	case "open_job":
		return k.OpenJob
	case "job_log":
		return k.JobLog
	case "rerun":
		return k.Rerun
	}
	return key.Binding{}
}

func New(cfg config.GitHubConfig, km config.Keymap, n notify.Notifier, st *store.Store) *View {
	return newView(cfg, km, n, st, false)
}

// NewReviews builds the Reviews tab: the PR view with its review-requested
// section as the entire list, for reviewing without your own PRs in the way.
func NewReviews(cfg config.GitHubConfig, km config.Keymap, n notify.Notifier, st *store.Store) *View {
	return newView(cfg, km, n, st, true)
}

func newView(cfg config.GitHubConfig, km config.Keymap, n notify.Notifier, st *store.Store, reviewsOnly bool) *View {
	bind := func(action, desc string, def ...string) key.Binding {
		return ui.Bind(km.Of("prs", action, def...), "", desc)
	}
	v := &View{
		cfg:      cfg,
		store:    st,
		notifier: n,
		list:     ui.NewList[pr](),
		// The Reviews tab has no own-PR search to clear the tab-wide flag,
		// so it starts on the review search's own.
		loading:       !reviewsOnly,
		reviewLoading: reviewsOnly,
		showReview:    reviewsOnly || cfg.ShowReviewRequestedOn(),
		reviewsOnly:   reviewsOnly,
		keys: viewKeys{
			Open:       bind("open", "open", "enter"),
			Copy:       bind("copy_url", "copy url", "y"),
			Diff:       bind("diff", "diff", "d"),
			Sort:       bind("sort", "sort", "s"),
			Rev:        bind("reverse", "reverse", "S"),
			Review:     bind("toggle_review", "review reqs", "w"),
			HideDrafts: bind("hide_drafts", "drafts", "D"),
			Start:      bind("review", "review", "r"),
			Comments:   bind("comments", "comments", "c"),
			NextThread: bind("next_thread", "", "]"),
			PrevThread: bind("prev_thread", "", "["),
			Reply:      bind("reply", "", "R"),
			Resolve:    bind("resolve", "", "X"),
			TopComment: bind("comment", "", "C"),
			Expand:     bind("expand", "expand", "e"),
			Jobs:       bind("jobs", "jobs", "t"),
			OpenJob:    bind("open_job", "", "o"),
			JobLog:     bind("job_log", "", "p"),
			Rerun:      bind("rerun", "", "x"),
			EditFilter: bind("edit_filter", "search", "F"),
		},
	}
	if mode, ok := sortByName(cfg.Sort); ok {
		v.sort = mode
	}
	v.rev = cfg.Reverse
	v.rowRefresh = cfg.RefreshRowEnabled()
	v.hideApproved = cfg.HideApproved
	v.hideDrafts = cfg.HideDrafts
	v.list.SetRowHeight(2) // two-line rows: metadata + title
	v.list.Rebind(func(a string, d ...string) []string { return km.Of("list", a, d...) })
	v.nav = newNavKeys(km)

	// Paint last run's PRs immediately; the live fetch refreshes them.
	cached, ok := cache.Load[cachedPRs](v.cacheName())
	if reviewsOnly {
		// Own PRs have no place here whatever an earlier run wrote: the
		// tab never fetches them, so a stale row would never be replaced.
		cached.Mine = nil
	}
	if ok && len(cached.Mine)+len(cached.Review) > 0 {
		v.raw, v.reviewRaw = cached.Mine, cached.Review
		v.seeded, v.mineSeeded = true, true
		if len(cached.Unread) > 0 {
			v.unread = make(map[string]bool, len(cached.Unread))
			for _, url := range cached.Unread {
				v.unread[url] = true
			}
		}
		v.applySort()
		v.publish(append(cached.Mine, cached.Review...))
		v.loading = false
	}
	return v
}

// cachedPRs is the on-disk shape of the last fetch. Unread rides along: a
// mark you never looked at has to survive a restart, or quitting silently
// marks everything read.
type cachedPRs struct {
	Mine   []pr     `json:"mine"`
	Review []pr     `json:"review"`
	Unread []string `json:"unread,omitempty"`
}

// unreadURLs is the unread set as a sorted slice, for a stable cache file.
func (v *View) unreadURLs() []string {
	if len(v.unread) == 0 {
		return nil
	}
	out := make([]string, 0, len(v.unread))
	for url := range v.unread {
		out = append(out, url)
	}
	sort.Strings(out)
	return out
}

// cacheName keeps each tab's rows apart: the two tabs fetch different
// searches, and sharing a file would let each overwrite the other's.
// DelegateReviews turns the PRs tab's review section over to a Reviews tab:
// the section is off, 'w' says why, and the review search is never run here.
func (v *View) DelegateReviews() *View {
	v.reviewsElsewhere = true
	v.showReview = false
	// Rows and marks the cache carried from before the handoff belong to
	// the other tab now; kept here they would be written back on every
	// save as a stale second copy.
	v.reviewRaw = nil
	for url := range v.unread {
		if !inPRs(v.raw, url) {
			delete(v.unread, url)
		}
	}
	v.applySort() // the list was built with the section on
	return v
}

func inPRs(set []pr, url string) bool {
	for _, p := range set {
		if p.URL == url {
			return true
		}
	}
	return false
}

// foreign reports a message another PR tab's command produced. The root
// model broadcasts data messages to every view and the PRs and Reviews tabs
// are the same type, so each would otherwise act on the other's results:
// take its rows, show its flash, close its popup, refetch on its filter
// trial. Shared caches keyed by PR URL are left to flow; nil means local,
// which is what tests send.
func (v *View) foreign(from *View) bool { return from != nil && from != v }

func (v *View) cacheName() string {
	if v.reviewsOnly {
		return "reviews"
	}
	return "prs"
}

func (v *View) Title() string {
	if v.reviewsOnly {
		return "Reviews"
	}
	return "PRs"
}

func (v *View) Init() tea.Cmd {
	v.loading = true
	// A refresh covers the jobs pane too: it is the retry for a fetch that
	// failed, and otherwise refetches what is on screen.
	if st, ok := v.jobs[v.list.Selected().URL]; ok && !st.inFlight {
		st.fetchedAt, st.failures = time.Time{}, 0
	}
	return tea.Batch(v.fetch(), v.maybeFetchJobs())
}

func (v *View) Loading() bool {
	return v.loading || v.reviewLoading || (v.review != nil && v.review.submitting)
}

// graphqlQuery is one PR search. The own-PRs and review-requested searches
// run as two separate requests; combining them into one aliased query makes
// GitHub's gateway time out (502) on real accounts.
const graphqlQuery = `query($q: String!, $n: Int!, $after: String) {
  search(query: $q, type: ISSUE, first: $n, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes { ...prFields }
  }
}
fragment prFields on PullRequest {
  number title url state isDraft updatedAt headRefName baseRefName
  additions deletions mergeable reviewDecision body
  viewerLatestReview { state }
  reviewRequests(first: 10) { nodes { requestedReviewer {
    ... on User { login }
    ... on Team { combinedSlug }
  } } }
  author { login }
  repository { nameWithOwner }
  comments { totalCount }
  labels(first: 6) { nodes { name color } }
  commits(last: 1) { nodes { commit { statusCheckRollup {
    state
    contexts(last: 1) {
      totalCount
      checkRunCount
      checkRunCountsByState { state count }
      statusContextCount
      statusContextCountsByState { state count }
    }
  } } } }
}`

// ensurePR appends "is:pr" to a search query when absent (the search API
// returns issues too under type:ISSUE).
func ensurePR(q string) string {
	if strings.Contains(q, "is:pr") {
		return q
	}
	return strings.TrimSpace(q + " is:pr")
}

// graphqlErr is a GraphQL error entry: the parts worth showing a user.
type graphqlErr struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// decodeSearch reads a gh graphql response envelope. GitHub answers a partly
// forbidden search with HTTP 200, the rows the token may read, and nulls for
// the rest, alongside an errors array explaining why; returning the rows and
// the reason together beats silently dropping either. ok is false when the
// bytes are not a GraphQL envelope at all.
// pageState is how far one section has been paged.
type pageState struct {
	cursor  string
	hasMore bool
	loading bool
	total   int
}

// searchPage is one decoded page: its rows, the total the search matched, and
// where the next page starts.
type searchPage struct {
	prs     []pr
	total   int
	cursor  string
	hasMore bool
}

func decodeSearch(out []byte) (page searchPage, err error, ok bool) {
	var env struct {
		Data struct {
			Search struct {
				IssueCount int `json:"issueCount"`
				PageInfo   struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []pr `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
		Errors []graphqlErr `json:"errors"`
	}
	if json.Unmarshal(out, &env) != nil {
		return searchPage{}, nil, false
	}
	sr := env.Data.Search
	p := searchPage{
		prs:     dropEmpty(sr.Nodes),
		total:   sr.IssueCount,
		cursor:  sr.PageInfo.EndCursor,
		hasMore: sr.PageInfo.HasNextPage,
	}
	return p, hiddenErr(env.Errors, len(sr.Nodes)), true
}

// hiddenErr summarises why some rows are missing. Identical messages collapse:
// one forbidden org yields one error per hidden row.
func hiddenErr(errs []graphqlErr, total int) error {
	if len(errs) == 0 {
		return nil
	}
	msg := errs[0].Message
	if strings.Contains(msg, "personal access token (classic)") {
		msg += "\nUnset GITHUB_TOKEN so gh uses its own login, or switch to a fine-grained token."
	}
	if len(errs) < total {
		return fmt.Errorf("%d of %d hidden: %s", len(errs), total, msg)
	}
	return fmt.Errorf("%s", msg)
}

// dropEmpty removes non-PR nodes, which type:ISSUE decodes as empty objects.
func dropEmpty(prs []pr) []pr {
	kept := prs[:0]
	for _, p := range prs {
		if p.Number != 0 {
			kept = append(kept, p)
		}
	}
	return kept
}

// searchPRs runs one gh search and decodes the PR nodes. GitHub's GraphQL
// gateway intermittently answers these searches with an HTML 5xx page (gh
// then reports `invalid character '<'`), so transient failures retry with a
// short backoff before surfacing.
func searchPRs(q string, size int, after string) (searchPage, error, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		// GitHub's gateway gives up at ~10s; anything past 30s is a hung gh.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		// No --jq: the errors array has to survive. GitHub answers a partly
		// forbidden search with 200, the readable rows, and nulls for the
		// rest, so dropping the envelope loses the reason they vanished.
		args := []string{"api", "graphql",
			"-f", "query=" + graphqlQuery,
			"-f", "q=" + q,
			"-F", "n=" + strconv.Itoa(size),
		}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		out, err := exec.CommandContext(ctx, "gh", args...).Output()
		cancel()
		if err != nil {
			// gh exits non-zero when GraphQL reports errors, but the body is
			// still on stdout: keep whatever rows came back.
			if page, gqlErr, ok := decodeSearch(out); ok {
				return page, nil, gqlErr
			}
			lastErr = cmdErr(err)
			continue
		}
		page, gqlErr, ok := decodeSearch(out)
		if !ok {
			lastErr = fmt.Errorf("parsing gh output: unexpected response")
			continue
		}
		return page, nil, gqlErr
	}
	if lastErr != nil && strings.Contains(lastErr.Error(), "invalid character '<'") {
		return searchPage{}, fmt.Errorf("GitHub returned an error page (transient 5xx), retry with ctrl+r"), nil
	}
	return searchPage{}, fmt.Errorf("gh api graphql: %w", lastErr), nil
}

func (v *View) fetch() tea.Cmd {
	if v.reviewsOnly {
		// No own-PR search will land to clear the tab-wide spinner; the
		// review search carries its own.
		v.loading = false
		cmds := []tea.Cmd{v.fetchReview()}
		if v.unreadSync {
			cmds = append(cmds, fetchReadThreads())
		}
		return tea.Batch(cmds...)
	}
	q := ensurePR(v.cfg.Filter)
	size := v.pageSize()
	cmds := []tea.Cmd{func() tea.Msg {
		page, err, hidden := searchPRs(q, size, "")
		if err == nil {
			err = hidden
		}
		return mineMsg{from: v, page: page, err: err}
	}}
	// The review search only runs when something consumes it: the visible
	// section, or review-request notifications. Otherwise the view does
	// exactly one search, as it originally did.
	if v.showReview || v.notifier != nil {
		cmds = append(cmds, v.fetchReview())
	}
	if v.unreadSync {
		cmds = append(cmds, fetchReadThreads())
	}
	return tea.Batch(cmds...)
}

// fetchReview runs just the review-requested search.
func (v *View) fetchReview() tea.Cmd {
	rq := ensurePR(v.cfg.ReviewQuery())
	size := v.pageSize()
	v.reviewLoading = true
	return func() tea.Msg {
		page, err, hidden := searchPRs(rq, size, "")
		if err == nil {
			err = hidden
		}
		return reviewListMsg{from: v, page: page, err: err}
	}
}

// pageSize is how many PRs one request asks for: the whole search at once
// when lazy paging is off, otherwise the configured page.
func (v *View) pageSize() int {
	if !v.cfg.LazyPagingEnabled() {
		return 100 // the search API's ceiling, the pre-paging behaviour
	}
	return v.cfg.ResolvedPageSize()
}

// fetchMore loads the next page of whichever section still has one. It is
// driven by the cursor reaching the end of the list, so it must be cheap to
// call repeatedly and do nothing when there is nothing left.
func (v *View) fetchMore() tea.Cmd {
	if !v.cfg.LazyPagingEnabled() {
		return nil
	}
	size := v.pageSize()
	// The review section sits below mine, so the end of the list is its end
	// when it is showing.
	if v.showReview && v.reviewPage.hasMore && !v.reviewPage.loading {
		v.reviewPage.loading = true
		rq, cursor := ensurePR(v.cfg.ReviewQuery()), v.reviewPage.cursor
		return func() tea.Msg {
			page, err, hidden := searchPRs(rq, size, cursor)
			if err == nil {
				err = hidden
			}
			return reviewListMsg{from: v, page: page, err: err, more: true}
		}
	}
	if v.minePage.hasMore && !v.minePage.loading {
		v.minePage.loading = true
		q, cursor := ensurePR(v.cfg.Filter), v.minePage.cursor
		return func() tea.Msg {
			page, err, hidden := searchPRs(q, size, cursor)
			if err == nil {
				err = hidden
			}
			return mineMsg{from: v, page: page, err: err, more: true}
		}
	}
	return nil
}

func (v *View) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case mineMsg:
		if v.foreign(msg.from) {
			return nil
		}
		before := v.list.Selected()
		v.loading = false
		v.minePage.loading = false
		// A partly forbidden search returns rows and an error together, so
		// show the rows and raise the reason the rest are missing. With no
		// rows at all but a cache on screen, the data is stale rather than
		// gone: say so instead of blanking the view.
		if msg.err != nil && len(msg.page.prs) == 0 {
			if len(v.raw) > 0 {
				return statusCmd(ui.SeverityWarn, fmt.Errorf(
					"showing cached PRs, refresh failed\n%s", msg.err))
			}
			v.err = msg.err
			return nil
		}
		var partial tea.Cmd
		if msg.err != nil {
			partial = statusCmd(ui.SeverityWarn, msg.err)
		}
		v.err = nil
		v.flash = ""
		if msg.more {
			v.raw = append(v.raw, msg.page.prs...)
		} else {
			v.markUnread(v.raw, msg.page.prs, v.mineSeeded)
			v.raw = msg.page.prs
		}
		v.minePage.cursor, v.minePage.hasMore = msg.page.cursor, msg.page.hasMore
		v.minePage.total = msg.page.total
		v.mineSeeded = true
		v.applySort()
		v.publish(append(v.raw, v.reviewRaw...))
		v.saveCache()
		if !msg.more {
			partial = tea.Batch(partial, v.departed(before, msg.page.prs))
		}
		return partial
	case reviewListMsg:
		if v.foreign(msg.from) {
			return nil
		}
		before := v.list.Selected()
		v.reviewLoading = false
		v.reviewPage.loading = false
		if msg.err != nil && len(msg.page.prs) == 0 {
			v.reviewErr = msg.err
			v.applySort() // the section separator shows the failure
			return nil
		}
		var partial tea.Cmd
		if msg.err != nil {
			partial = statusCmd(ui.SeverityWarn, msg.err)
		}
		v.reviewErr = nil
		next := msg.page.prs
		if msg.more {
			next = append(v.reviewRaw, msg.page.prs...)
		}
		// Only a first page is a fresh set to compare against: a later page
		// is all new by definition and would notify for every row.
		var cmd tea.Cmd
		if !msg.more {
			v.markUnread(v.reviewRaw, next, v.seeded)
			cmd = v.notifyNewReviews(v.reviewRaw, next)
		}
		if partial != nil {
			cmd = tea.Batch(cmd, partial)
		}
		v.reviewRaw = next
		v.reviewPage.cursor, v.reviewPage.hasMore = msg.page.cursor, msg.page.hasMore
		v.reviewPage.total = msg.page.total
		v.seeded = true
		v.applySort()
		v.publish(append(v.raw, next...))
		v.saveCache()
		if !msg.more {
			cmd = tea.Batch(cmd, v.departed(before, next))
		}
		return cmd
	case filesMsg:
		st := v.files[msg.url]
		if st == nil {
			return nil
		}
		st.files, st.err, st.done = msg.files, msg.err, true
		v.bodyKey = ""
		if msg.err != nil {
			return statusCmd(ui.SeverityWarn, msg.err)
		}
		return nil
	case diffMsg:
		// Only a diff this tab asked for: the message reaches every PR tab,
		// and one that never fetched has no map to write into.
		if _, started := v.diffs[msg.url]; started {
			v.diffs[msg.url] = diffState{text: msg.text, err: msg.err, done: true}
		}
		return nil
	case settleMsg:
		if v.foreign(msg.from) {
			return nil
		}
		if msg.gen != v.settleGen {
			return nil // superseded by further navigation
		}
		return tea.Batch(v.maybeFetchDiff(), v.maybeFetchComments(), v.maybeFetchJobs(), v.maybeFetchLogs(), v.maybeFetchFiles())
	case logMsg:
		return v.applyLog(msg)
	case jobsMsg:
		return v.applyJobs(msg)
	case jobsTickMsg:
		if v.foreign(msg.from) {
			return nil
		}
		// Superseded, or the cursor moved on: the watch follows the PR in
		// front of you and stops when you leave it.
		if msg.gen != v.jobsGen || msg.url != v.list.Selected().URL {
			return nil
		}
		return v.maybeFetchJobs()
	case rerunDoneMsg:
		if v.foreign(msg.from) {
			return nil
		}
		return v.applyRerun(msg)
	case rowSettleMsg:
		if v.foreign(msg.from) {
			return nil
		}
		// Superseded, or the cursor moved on: the row that asked for this
		// is no longer the one in front of you.
		if msg.gen != v.rowGen || msg.url != v.list.Selected().URL {
			return nil
		}
		return v.refreshRow(msg.url)
	case rowFreshMsg:
		if !msg.ok || !v.applyFresh(msg.pr) {
			return nil
		}
		before := v.list.Selected()
		v.applySort()
		v.bodyKey = "" // checks and review state render in the preview
		return v.departed(before, []pr{msg.pr})
	case mergeDoneMsg:
		if v.foreign(msg.from) {
			return nil
		}
		// The popup stays up with the outcome; the next key closes it.
		r := v.review
		if r != nil {
			r.submitting = false
		}
		if msg.err != nil {
			if r != nil {
				r.fail = "merge failed: " + msg.err.Error()
			}
			v.flash = ui.Red.Render("merge failed: " + msg.err.Error())
			return statusCmd(ui.SeverityError, msg.err)
		}
		if r != nil {
			r.done = msg.what
		}
		v.flash = ui.Green.Render("✓ " + msg.what)
		// A merged PR leaves the search on the next fetch; auto-merge
		// leaves it open, so only refetch and let the row speak for itself.
		v.resetToggles()
		return tea.Batch(ui.ConcealPreview, v.fetch())
	case reviewDoneMsg:
		if v.foreign(msg.from) {
			return nil
		}
		v.review = nil
		if msg.err != nil {
			v.flash = ui.Red.Render("review failed: " + msg.err.Error())
			return nil
		}
		v.flash = ui.Green.Render("✓ " + msg.what)
		// Optimistically record the review so the row dims the moment the
		// popup closes; the refetch below confirms it.
		for i := range v.reviewRaw {
			if v.reviewRaw[i].URL == msg.url {
				v.reviewRaw[i].ViewerLatestReview.State = msg.state
			}
		}
		v.applySort()
		// Review submitted: you are done with this one, so the diff or
		// comments pane you opened to review it folds away with the reveal.
		v.resetToggles()
		return tea.Batch(ui.ConcealPreview, v.fetch())
	case commentsMsg:
		if st, ok := v.comments[msg.url]; ok {
			st.data, st.err, st.done = msg.data, msg.err, true
			v.commentsRev++
		}
		return nil
	case ui.TogglesPersistMsg:
		v.togglesPersist = bool(msg)
		return nil
	case ui.SpinnerTickMsg:
		v.spinFrame = msg.Frame
		return nil
	case ui.ListJumpMsg:
		v.jump = int(msg)
		v.list.SetJump(v.jump)
		return nil
	case ui.UnreadMsg:
		// Display only: marks keep being recorded while this is off, so
		// turning it on shows what arrived in the meantime.
		v.unreadOn = bool(msg)
		v.applySort()
		return nil
	case ui.UnreadSyncMsg:
		v.unreadSync = bool(msg)
		return nil
	case threadsReadMsg:
		// A notification read elsewhere (github.com, another client) clears
		// the mark here, so the two agree in both directions.
		if !v.unreadSync || len(v.unread) == 0 {
			return nil
		}
		cleared := false
		for url := range v.unread {
			p, ok := v.prByURL(url)
			if !ok || p.repo() == "" || p.Number == 0 {
				continue
			}
			if msg.read[fmt.Sprintf("%s#%d", p.repo(), p.Number)] {
				delete(v.unread, url)
				cleared = true
			}
		}
		if cleared {
			v.applySort()
			v.saveCache()
		}
		return nil
	case ui.PreviewShownMsg:
		v.previewShown = bool(msg)
		v.floatFocus()
		// Revealing the detail shows whatever is selected, so that row is
		// read.
		if v.previewShown {
			v.clearUnread()
		}
		// Labels live in the space a hidden preview frees up.
		v.applySort()
		return v.drainSync()
	case ui.PreviewFloatingMsg:
		v.floatReveal = bool(msg)
		v.floatFocus()
		return nil
	case ui.GroupingMsg:
		v.grouping = bool(msg)
		v.applySort()
		return nil
	case filterTriedMsg:
		if v.foreign(msg.from) {
			return nil
		}
		if msg.badAuthor() {
			// Put the filter back and say why, rather than persisting a
			// query that empties the list: the editor would then be the
			// only way out, and a restart would bring it back.
			v.restoreFilter(msg.path, msg.prev)
			v.loading = false
			v.applySort()
			return statusCmd(ui.SeverityWarn, fmt.Errorf(
				"filter not saved: %s matches nothing, but does without its "+
					"author terms. GitHub resolves author: against real "+
					"accounts, and a name it cannot find voids the whole "+
					"query (bots are app/<name>, e.g. app/renovate)",
				msg.query))
		}
		if msg.err != nil {
			v.restoreFilter(msg.path, msg.prev)
			v.loading = false
			v.applySort()
			return statusCmd(ui.SeverityError, msg.err)
		}
		// It works: keep it, and load it properly.
		return tea.Batch(
			func() tea.Msg { return ui.ConfigSetMsg{Path: msg.path, Value: msg.query} },
			v.fetch(),
		)
	case threadDoneMsg:
		if v.foreign(msg.from) {
			return nil
		}
		v.input = nil
		if msg.err != nil {
			v.flash = ui.Red.Render("failed: " + msg.err.Error())
			return nil
		}
		v.flash = ui.Green.Render("✓ " + msg.what)
		// Refetch the PR the mutation targeted (the selection may have
		// moved while gh ran), so its pane isn't stale when revisited.
		delete(v.comments, msg.url)
		if p, ok := v.prByURL(msg.url); ok {
			v.comments[p.URL] = &commentsState{}
			return v.fetchCommentsCmd(p)
		}
		return v.maybeFetchComments()
	case tea.KeyMsg:
		if v.review != nil {
			return v.updateReview(msg)
		}
		if v.rerun != nil {
			return v.updateRerun(msg)
		}
		if v.input != nil {
			return v.updateThreadInput(msg)
		}
		// The jobs pane takes the movement keys while it has focus, and
		// right arrow gives it focus: it is the pane on the right.
		if v.pane == paneJobs && !v.list.Filtering() {
			if v.logView != nil {
				if cmd, ok := v.updateLogKeys(msg); ok {
					return cmd
				}
			}
			if v.jobsFocus {
				if cmd, ok := v.updateJobsFocus(msg); ok {
					return cmd
				}
			} else if msg.String() == "right" {
				v.jobsFocus = true
				return ui.RevealPreview
			}
		}
		// The file list holds its own cursor, like the jobs pane.
		if v.pane == paneFiles && !v.list.Filtering() {
			if v.paneFocus {
				if cmd, ok := v.updateFiles(msg); ok {
					return cmd
				}
			} else if msg.String() == "right" {
				v.paneFocus = true
				return ui.RevealPreview
			}
		}
		// Floated, an arrow that would leave the pane steps a level out of
		// the float. The file list and jobs pane collapse first and do this
		// themselves.
		if v.floatReveal && v.pane != paneFiles && v.pane != paneJobs && !v.list.Filtering() {
			switch msg.String() {
			case "left":
				return v.leaveFloat()
			case "right":
				return nil // nothing deeper to step into
			}
		}
		// Beside a visible list the same step applies one level down: with
		// the keys already on the list, left takes an open pane back to the
		// description. The configured pane itself never closes.
		if v.previewShown && !v.floatReveal && !v.PaneFocused() && v.pane != paneBody &&
			!v.list.Filtering() && msg.String() == "left" {
			v.pane = paneBody
			v.logView = nil
			return nil
		}
		// Every other pane takes focus the same way: right arrow in, left
		// arrow back to the list. These scroll rather than holding a
		// cursor, so the root model routes the arrows to the preview.
		if v.pane == paneDiff || v.pane == paneComments {
			switch {
			case !v.paneFocus && msg.String() == "right":
				v.paneFocus = true
				return ui.RevealPreview
			case v.previewShown && v.paneFocus && msg.String() == "left":
				v.paneFocus = false
				return nil
			}
		}
		if v.filterEd != nil {
			return v.updateFilterEdit(msg)
		}
		before := v.list.Selected().URL
		if consumed, cmd := v.list.Update(msg); consumed {
			// Selection may have moved while a data pane is showing; fetch
			// for the new selection only once it settles, so holding j/k
			// doesn't spawn a gh call per row scrolled past.
			v.annIdx = 0
			// Reaching the end is the signal to load the next page, so the
			// first paint stays one fast request.
			var more tea.Cmd
			if v.list.AtEnd() {
				more = v.fetchMore()
			}
			if v.list.Selected().URL != before {
				// Moving on ends a transient preview reveal and resets the
				// toggles it carried. It reads the row being left, not the
				// one arrived at, and only when its detail was on screen;
				// hidden, you have not seen anything yet.
				v.resetToggles()
				// A float ends here, so the row arrived at is not on screen
				// yet; a pane that stays open does show it.
				if v.previewShown {
					v.clearUnreadFor(before)
					if !v.floatReveal {
						v.clearUnread()
					}
				}
				return tea.Batch(cmd, v.scheduleSettle(), v.scheduleRowRefresh(), ui.ConcealPreview, more, v.drainSync())
			}
			return tea.Batch(cmd, v.scheduleSettle(), more)
		}
		if v.list.Filtering() {
			return nil
		}
		switch {
		case key.Matches(msg, v.keys.Open):
			return v.openSelected()
		case key.Matches(msg, v.keys.Copy):
			return v.copySelected()
		case key.Matches(msg, v.keys.Diff):
			// Default 'd' keeps the original behavior: page the diff
			// through less. github.diff_pane opts into the in-pane view,
			// which is the file list: it reads better than a flat diff and
			// is the only one that works past 300 files.
			if !v.cfg.DiffPane {
				return v.diffInPager()
			}
			return v.setPane(paneFiles)
		case key.Matches(msg, v.keys.Expand):
			if sel := v.list.Selected(); sel.URL != "" {
				if v.expanded == sel.URL {
					v.expanded = ""
				} else {
					v.expanded = sel.URL
				}
				v.bodyKey = "" // the body is cached per expansion state
			}
			return nil
		case key.Matches(msg, v.keys.EditFilter):
			return v.editFilter()
		case key.Matches(msg, v.keys.Comments):
			return v.setPane(paneComments)
		case key.Matches(msg, v.keys.Jobs):
			return v.setPane(paneJobs)
		case key.Matches(msg, v.keys.OpenJob):
			return v.openJob()
		case key.Matches(msg, v.keys.JobLog):
			return v.jobLogInPager()
		case key.Matches(msg, v.keys.Rerun):
			return v.openRerun()
		case key.Matches(msg, v.keys.NextThread):
			if v.pane == paneJobs {
				return v.jumpFailed(1)
			}
			return v.jumpThread(1)
		case key.Matches(msg, v.keys.PrevThread):
			if v.pane == paneJobs {
				return v.jumpFailed(-1)
			}
			return v.jumpThread(-1)
		case key.Matches(msg, v.keys.Reply):
			if t, ok := v.currentThread(); ok {
				v.input = &threadFlow{kind: "reply", threadID: t.ID, target: t.Path + lineSuffix(t)}
			}
			return nil
		case key.Matches(msg, v.keys.Resolve):
			if t, ok := v.currentThread(); ok {
				return toggleResolve(v, v.list.Selected().URL, t.ID, t.IsResolved)
			}
			return nil
		case key.Matches(msg, v.keys.TopComment):
			if p := v.list.Selected(); p.URL != "" {
				v.input = &threadFlow{kind: "comment", target: fmt.Sprintf("%s#%d", p.repo(), p.Number)}
			}
			return nil
		case key.Matches(msg, v.keys.Sort):
			v.sort = sortOrder[(int(v.sort)+1)%len(sortOrder)]
			v.applySort()
			return nil
		case key.Matches(msg, v.keys.Rev):
			v.rev = !v.rev
			v.applySort()
			return nil
		case key.Matches(msg, v.keys.Review) && (v.reviewsOnly || v.reviewsElsewhere):
			// Not silent: someone who bound the key will press it and
			// expect something to happen.
			return func() tea.Msg {
				return ui.ToastMsg{Title: "Review requests", Body: "Toggle disabled while 'Reviews' view is active"}
			}
		case key.Matches(msg, v.keys.HideDrafts):
			v.hideDrafts = !v.hideDrafts
			v.applySort()
			return nil
		case key.Matches(msg, v.keys.Review):
			v.showReview = !v.showReview
			v.applySort()
			// The review search isn't fetched while the section is off
			// (and notifications don't need it), so backfill on demand.
			if v.showReview && len(v.reviewRaw) == 0 && !v.reviewLoading {
				return v.fetchReview()
			}
			return nil
		case key.Matches(msg, v.keys.Start):
			if p := v.list.Selected(); p.URL != "" {
				v.review = &reviewFlow{url: p.URL, repo: p.repo(), num: p.Number}
				// Reviewing reads better against the change, where the pane
				// is enabled: the file list, or the flat diff by config.
				// Through setPane, so the pane is in the same state as one
				// toggled by hand once the popup goes.
				if v.cfg.DiffPane && v.pane == paneBody {
					return v.setPane(v.reviewPane())
				}
				return tea.Batch(v.maybeFetchDiff(), v.maybeFetchComments(), v.maybeFetchFiles())
			}
		}
	}
	return nil
}

// resetToggles returns the per-item view toggles to what the config asks
// for. A toggle belongs to the item it was pressed on: leaving one behind
// means every later PR opens in a pane you chose for a different one.
func (v *View) resetToggles() {
	if v.togglesPersist {
		return
	}
	v.pane = paneBody
	v.jobsFocus, v.paneFocus, v.logView = false, false, nil
	v.expanded = ""
	v.bodyKey = ""
	v.annIdx = 0
}

// setPane toggles the right pane between the description and the given mode,
// kicking off whatever fetch that pane needs.
func (v *View) setPane(mode paneMode) tea.Cmd {
	v.jobsFocus, v.logView = false, nil
	if v.pane == mode {
		v.pane = paneBody
		// Back to the description, not away from the detail: concealing
		// here would shut a floated preview instead of switching panes.
		// Only a pane the toggle itself revealed goes away again.
		if v.floatReveal {
			// Toggling the pane off is leaving its level: back to a 'v'
			// description if there is one, otherwise the float closes.
			v.pane = mode
			return v.leaveFloat()
		}
		if v.previewShown {
			return nil
		}
		return ui.ConcealPreview
	}
	v.pane = mode
	v.annIdx = 0
	// A pane opened into a float takes the keys straight away: the list is
	// behind it, so there is nothing to arrow through and an explicit
	// right would be a keystroke for nothing. With the preview pane on,
	// both are visible, so focus stays with the list until asked for.
	// floatReveal as well as !previewShown: a pane toggled over a 'v' float
	// has the detail on screen already, and is just as much behind glass.
	auto := !v.previewShown || v.floatReveal || mode == paneJobs
	v.jobsFocus = auto && mode == paneJobs
	v.paneFocus = auto && mode != paneJobs && mode != paneBody
	// Opening a pane with the preview hidden is the float appearing. Say so
	// here rather than waiting for the root model's toggle message, which
	// only fires when floating() changes.
	v.floatReveal = v.floatReveal || !v.previewShown
	// The pane is about to show a diff, comments or jobs; a hidden preview
	// would swallow it silently.
	return tea.Batch(ui.RevealPreview, v.maybeFetchDiff(), v.maybeFetchComments(), v.maybeFetchJobs(), v.maybeFetchLogs(), v.maybeFetchFiles())
}

// floatFocus settles focus from the preview's combined state: a float over
// the list takes the keys, a side pane or an empty screen leaves them with
// the list. Both preview messages call it because tea.Batch runs its
// commands concurrently, so they land in either order.
func (v *View) floatFocus() {
	if v.floatReveal && v.previewShown {
		v.paneFocus = true
		v.jobsFocus = v.pane == paneJobs
		// A float that settles on the description was opened with 'v', so
		// a pane toggled over it has that level to step back to. A pane
		// opened straight from the list is level one itself.
		if v.pane == paneBody {
			v.floatBase = true
		}
		return
	}
	v.jobsFocus, v.paneFocus, v.logView = false, false, nil
	if !v.floatReveal {
		v.floatBase = false
	}
}

// leaveFloat steps one level out of a float: a pane over a 'v' description
// goes back to it, anything else closes the float. There is no list beside
// a float to hand the keys to, so leaving the last level is closing.
func (v *View) leaveFloat() tea.Cmd {
	if v.floatBase && v.pane != paneBody {
		v.pane = paneBody
		v.jobsFocus, v.logView = false, nil
		v.paneFocus = true // the description scrolls, so it keeps the keys
		return nil
	}
	return v.closeFloat()
}

// closeFloat shuts a floated detail from inside, whatever level it is on.
func (v *View) closeFloat() tea.Cmd {
	v.pane = paneBody
	v.jobsFocus, v.paneFocus, v.logView = false, false, nil
	v.floatReveal, v.floatBase = false, false
	return ui.ConcealPreview
}

// jumpThread moves between inline-thread anchors in the current pane and
// asks the root model to scroll the preview there.
func (v *View) jumpThread(d int) tea.Cmd {
	if v.pane == paneBody || len(v.anchors) == 0 {
		return nil
	}
	v.annIdx = (v.annIdx + d + len(v.anchors)) % len(v.anchors)
	line := v.anchors[v.annIdx].Line + v.paneHeader
	v.pendingJump = &line
	return ui.RevealPreview
}

// TakePreviewJump implements the root model's preview-jump hook: it returns
// the rendered line the preview should scroll to, at most once per request.
func (v *View) TakePreviewJump() (int, bool) {
	if v.pendingJump == nil {
		return 0, false
	}
	line := *v.pendingJump
	v.pendingJump = nil
	return line, true
}

type reviewDoneMsg struct {
	from *View
	what string
	url  string
	// state is the ViewerLatestReview state the verdict implies, applied
	// optimistically so the row dims before the refetch lands.
	state string
	err   error
}

// updateReview handles keys while the review popup is open: pick an option
// (cursor or hotkey), then a body for the verdicts that need one, then
// submit.
func (v *View) updateReview(msg tea.KeyMsg) tea.Cmd {
	r := v.review
	if r.done != "" || r.fail != "" {
		v.review = nil // any key dismisses the outcome
		return nil
	}
	if r.submitting {
		return nil // ignore keys while gh runs
	}
	// A merge waits on an explicit yes. Only "y" proceeds: enter is what
	// selected the entry, so accepting it here would merge on one keypress.
	if r.confirm != "" {
		switch msg.String() {
		case "y":
			what := r.confirm
			r.confirm, r.warn = "", ""
			if what == "approve" {
				return v.submitReview("approve")
			}
			return v.submitMerge(what == "auto")
		default:
			r.confirm, r.warn = "", ""
			return nil
		}
	}
	if r.verdict == "" {
		opts := v.options()
		switch msg.String() {
		case "up", "k":
			r.sel, r.blocked = (r.sel-1+len(opts))%len(opts), ""
			return nil
		case "down", "j":
			r.sel, r.blocked = (r.sel+1)%len(opts), ""
			return nil
		case "enter":
			return v.activateReviewOption(opts[r.sel].label)
		case "esc", "q", "ctrl+c":
			v.review = nil
			return nil
		default:
			for _, opt := range opts {
				if opt.key != "" && msg.String() == opt.key {
					return v.activateReviewOption(opt.label)
				}
			}
			return nil
		}
	}
	switch msg.String() {
	case "esc":
		r.verdict, r.body = "", "" // back to the verdict picker
	case "enter":
		if strings.TrimSpace(r.body) == "" {
			return nil // GitHub requires a body for these verdicts
		}
		return v.submitReview(r.verdict)
	case "backspace":
		if r.body != "" {
			r.body = r.body[:len(r.body)-1]
		}
	default:
		if kp, ok := tea.Msg(msg).(tea.KeyPressMsg); ok && kp.Text != "" {
			if ru := []rune(kp.Text)[0]; ru >= 0x20 && ru != 0x7f {
				r.body += kp.Text
			}
		}
	}
	return nil
}

// activateReviewOption runs one popup entry by label.
func (v *View) activateReviewOption(label string) tea.Cmd {
	r := v.review
	switch label {
	case "Approve":
		// Already reviewed this one: say so rather than silently stacking a
		// second approval on top of the first.
		if p, ok := v.prByURL(r.url); ok && p.reviewedByMe() {
			r.confirm, r.warn = "approve", reviewedNote(p.ViewerLatestReview.State)
			return nil
		}
		return v.submitReview("approve")
	case "Comment":
		r.verdict = "comment"
	case "Request changes":
		r.verdict = "request-changes"
	case "View diff":
		// Show the change and get out of the way; 'r' reopens the popup.
		v.review = nil
		if !v.cfg.DiffPane {
			return v.diffInPager()
		}
		target := v.reviewPane()
		if v.pane == target {
			// 'r' opened it behind the popup already: hand it the keys.
			v.paneFocus = true
			return ui.RevealPreview
		}
		return v.setPane(target)
	case mergeLabel:
		v.askMerge("merge")
	case autoLabel:
		v.askMerge("auto")
	case "Cancel":
		v.review = nil
	}
	return nil
}

// reviewPane is the pane a review reads against: the file list, or the
// flat diff when review_view asks for it.
func (v *View) reviewPane() paneMode {
	if v.cfg.ResolvedReviewView() == "unified" {
		return paneDiff
	}
	return paneFiles
}

// reviewedNote says what the viewer's standing review on a PR is, for the
// confirmation that catches a second one.
func reviewedNote(state string) string {
	switch state {
	case "APPROVED":
		return "you have already approved this"
	case "CHANGES_REQUESTED":
		return "you requested changes on this"
	case "COMMENTED":
		return "you have already commented on this"
	}
	return "you have already reviewed this"
}

// merging reports whether the popup is on a merge: staged, running or done.
func (r *reviewFlow) merging() bool {
	switch {
	case r.confirm == "merge", r.confirm == "auto", r.action == "merge", r.action == "auto":
		return true
	}
	return false
}

// mergeDetail is the method and branch fate, before or after the fact.
func (v *View) mergeDetail(past bool) string {
	detail := v.cfg.ResolvedMergeMethod()
	switch {
	case !v.cfg.MergeDeleteBranch:
	case past:
		detail += ", branch deleted"
	default:
		detail += ", then delete the branch"
	}
	return detail
}

// mergeTitle is the selected PR's title, so the confirmation names what is
// about to land rather than a bare number.
func (v *View) mergeTitle(url string) string {
	if p, ok := v.prByURL(url); ok {
		return p.Title
	}
	return ""
}

// askMerge stages a merge for confirmation, or refuses it outright when
// GitHub already says it cannot land. A plain merge needs the PR to be
// mergeable now; auto-merge is for the ones that are not yet, so it only
// refuses on the states waiting will not fix.
func (v *View) askMerge(kind string) {
	r := v.review
	p, ok := v.prByURL(r.url)
	if !ok {
		r.blocked = "this PR is no longer loaded; refresh and try again"
		return
	}
	switch {
	case p.IsDraft:
		r.blocked = "it is a draft: mark it ready first"
		return
	case p.Mergeable == "CONFLICTING":
		r.blocked = "it has conflicts to resolve first"
		return
	case kind == "merge" && p.Mergeable == "UNKNOWN":
		r.blocked = "GitHub has not finished checking mergeability; try again shortly"
		return
	}
	r.blocked = ""
	r.confirm = kind
	// Say what is off about it rather than refusing: these are judgement
	// calls, and the repo's own rules are what actually gate the merge.
	switch p.ReviewDecision {
	case "CHANGES_REQUESTED":
		r.warn = "changes have been requested"
	case "REVIEW_REQUIRED":
		r.warn = "it has not been approved yet"
	default:
		if _, fail, _, _ := p.checkCounts(); fail > 0 {
			r.warn = "checks are failing"
		}
	}
}

// Overlay implements the root model's view-modal hook: the rerun or review
// popup.
func (v *View) Overlay() string {
	if v.rerun != nil {
		return v.rerunOverlay()
	}
	r := v.review
	if r == nil {
		return ""
	}

	var b strings.Builder
	// The heading follows the step: "Review" is wrong above a merge.
	heading := "Review"
	if r.merging() {
		heading = "Merge"
	}
	b.WriteString(ui.Bold.Render(fmt.Sprintf("%s %s#%d", heading, r.repo, r.num)))
	b.WriteString("\n\n")
	switch {
	case r.done != "":
		b.WriteString(ui.Green.Render("✓ "+r.done) + "\n")
		if r.action == "merge" {
			b.WriteString(ui.Faint.Render("  "+v.mergeDetail(true)) + "\n")
		}
		b.WriteString("\n" + ui.Dim.Render("any key to close"))
	case r.fail != "":
		b.WriteString(ui.Red.Render("✗ "+r.fail) + "\n\n")
		b.WriteString(ui.Dim.Render("any key to close"))
	case r.submitting:
		verb := "submitting review…"
		switch r.action {
		case "merge":
			verb = "merging…"
		case "auto":
			verb = "enabling auto-merge…"
		}
		b.WriteString(ui.SpinnerFrame(v.spinFrame) + " " + ui.Faint.Render(verb) + "\n")
		if r.action == "merge" {
			b.WriteString(ui.Faint.Render("  " + v.mergeDetail(false)))
		}
	case r.confirm != "":
		what := "Merge this PR?"
		switch r.confirm {
		case "auto":
			what = "Merge this PR once its checks pass?"
		case "approve":
			what = "Approve it again?"
		}
		b.WriteString(what + "\n")
		if title := v.mergeTitle(r.url); title != "" {
			b.WriteString(ui.Dim.Render("  "+title) + "\n")
		}
		if r.confirm != "approve" {
			b.WriteString(ui.Faint.Render("  "+v.mergeDetail(false)) + "\n")
		}
		if r.warn != "" {
			b.WriteString("\n" + ui.Yellow.Render("! "+r.warn) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(ui.Dim.Render("y to confirm · any other key cancels"))
	case r.verdict == "":
		if r.blocked != "" {
			b.WriteString(ui.Yellow.Render("! Cannot merge: ") +
				r.blocked + "\n\n")
		}
		for i, opt := range v.options() {
			cursor := "  "
			label := opt.label
			if i == r.sel {
				cursor = ui.Accent.Render("› ")
				label = ui.Accent.Render(label)
			}
			hot := "   "
			if opt.key != "" {
				hot = ui.Bold.Render(opt.key) + "  "
			}
			b.WriteString(cursor + hot + label)
			b.WriteByte('\n')
		}
		b.WriteString("\n")
		b.WriteString(ui.Dim.Render("↑↓ move · enter select · esc close"))
	default:
		label := r.verdict
		if label == "request-changes" {
			label = "request changes"
		}
		b.WriteString(ui.Yellow.Render(label + ": "))
		b.WriteString(r.body + "█")
		b.WriteString("\n\n")
		b.WriteString(ui.Dim.Render("enter submit · esc back"))
	}

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Pal().Accent)).
		Padding(0, 2).
		Render(b.String())
}

// mergeDoneMsg reports the outcome of a merge.
type mergeDoneMsg struct {
	from *View
	what string
	url  string
	// auto is set when this enabled auto-merge rather than merging now, so
	// the row is not struck through for a PR that is still open.
	auto bool
	err  error
}

// submitMerge shells out to gh pr merge. The method comes from config, and
// gh reports a repo that forbids it rather than agenda guessing.
func (v *View) submitMerge(auto bool) tea.Cmd {
	r := v.review
	r.submitting, r.action = true, "merge"
	if auto {
		r.action = "auto"
	}
	args := []string{"pr", "merge", strconv.Itoa(r.num), "-R", r.repo,
		"--" + v.cfg.ResolvedMergeMethod()}
	if auto {
		args = append(args, "--auto")
	}
	if v.cfg.MergeDeleteBranch {
		args = append(args, "--delete-branch")
	}
	what := fmt.Sprintf("merged %s#%d", r.repo, r.num)
	if auto {
		what = fmt.Sprintf("auto-merge enabled on %s#%d", r.repo, r.num)
	}
	url := r.url
	return func() tea.Msg {
		if out, err := exec.Command("gh", args...).CombinedOutput(); err != nil {
			return mergeDoneMsg{from: v, err: ghErr(err, out), auto: auto}
		}
		return mergeDoneMsg{from: v, what: what, url: url, auto: auto}
	}
}

// ghErr prefers gh's own message over the bare exit status: "not
// mergeable" or a forbidden method is the useful part, and cmdErr would
// reduce it to "exit status 1".
func ghErr(err error, out []byte) error {
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "X"))
		if line != "" && !strings.HasPrefix(line, "!") {
			return errors.New(line)
		}
	}
	return cmdErr(err)
}

// submitReview shells out to gh pr review with the flow's verdict and body.
func (v *View) submitReview(verdict string) tea.Cmd {
	r := v.review
	r.submitting, r.action = true, "review"
	args := []string{"pr", "review", strconv.Itoa(r.num), "-R", r.repo, "--" + verdict}
	if strings.TrimSpace(r.body) != "" {
		args = append(args, "--body", r.body)
	}
	var what, state string
	switch verdict {
	case "approve":
		what, state = fmt.Sprintf("approved %s#%d", r.repo, r.num), "APPROVED"
	case "comment":
		what, state = fmt.Sprintf("commented on %s#%d", r.repo, r.num), "COMMENTED"
	default:
		what, state = fmt.Sprintf("requested changes on %s#%d", r.repo, r.num), "CHANGES_REQUESTED"
	}
	url := r.url
	return func() tea.Msg {
		if err := exec.Command("gh", args...).Run(); err != nil {
			return reviewDoneMsg{from: v, err: cmdErr(err)}
		}
		return reviewDoneMsg{from: v, what: what, url: url, state: state}
	}
}

// Refs implements ui.Referencer: the Linear issues this PR points at, plus the
// agent sessions that mention this PR (sourced from the shared store).
func (v *View) Refs() []ui.Ref {
	sel := v.list.Selected()
	var refs []ui.Ref
	for _, id := range sel.linearRefs() {
		var title, url string
		if v.store != nil {
			if iss, ok := v.store.Issue(id); ok {
				title, url = iss.Title, iss.URL
			}
		}
		refs = append(refs, ui.IssueRef(id, title, url))
	}
	if v.store != nil && sel.URL != "" {
		for _, s := range v.store.SessionsMentioning(store.Key("pr", sel.URL)) {
			refs = append(refs, ui.SessionRef(s.Path, s.Tool, s.Cwd, s.Title, s.Snippet))
		}
	}
	return refs
}

// publish pushes the loaded PRs' status into the shared store so other views
// (Linear) can render CI/review/merge icons for PRs they reference.
func (v *View) publish(prs []pr) {
	if v.store == nil {
		return
	}
	recs := make([]store.PR, 0, len(prs))
	for _, p := range prs {
		recs = append(recs, store.PR{
			URL:          p.URL,
			Repo:         p.repo(),
			Number:       p.Number,
			Title:        p.Title,
			State:        prState(p),
			CI:           ciState(p),
			Review:       reviewState(p),
			HasConflicts: p.Mergeable == "CONFLICTING",
			UpdatedAt:    p.UpdatedAt,
		})
	}
	v.store.PutPRs(recs)
}

func prState(p pr) store.PRState {
	switch {
	case p.IsDraft:
		return store.PRDraft
	case p.State == "MERGED":
		return store.PRMerged
	case p.State == "CLOSED":
		return store.PRClosed
	default:
		return store.PROpen
	}
}

func ciState(p pr) store.CIState {
	switch p.ciState() {
	case "SUCCESS":
		return store.CIPassing
	case "FAILURE", "ERROR":
		return store.CIFailing
	case "PENDING", "EXPECTED":
		return store.CIPending
	default:
		return store.CIUnknown
	}
}

func reviewState(p pr) store.ReviewState {
	switch p.ReviewDecision {
	case "APPROVED":
		return store.ReviewApproved
	case "CHANGES_REQUESTED":
		return store.ReviewChanges
	case "REVIEW_REQUIRED":
		return store.ReviewPending
	default:
		return store.ReviewNone
	}
}

// RefKind / HasRef / SelectRef implement ui.RefTarget so other views (e.g.
// Linear) can jump to a PR here. PRs are keyed by URL.
func (v *View) RefKind() string { return "pr" }

func matchURL(url string) func(pr) bool {
	return func(p pr) bool { return p.URL == url }
}

func (v *View) HasRef(id string) bool    { return v.list.Any(matchURL(id)) }
func (v *View) SelectRef(id string) bool { return v.list.Select(matchURL(id)) }

func (v *View) openSelected() tea.Cmd {
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	return func() tea.Msg {
		_ = exec.Command("gh", "pr", "view", "--web",
			strconv.Itoa(p.Number), "-R", p.repo()).Start()
		return nil
	}
}

func (v *View) copySelected() tea.Cmd { return ui.CopyCmd(v.list.Selected().URL, "URL") }

// diffInPager pages the selected PR's diff through less, the view's original
// 'd' behavior (default; github.diff_pane opts into the in-pane diff).
func (v *View) diffInPager() tea.Cmd {
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	c := exec.Command("sh", "-c",
		fmt.Sprintf("gh pr diff %d -R %s | less -R", p.Number, p.repo()))
	return tea.ExecProcess(c, func(error) tea.Msg { return nil })
}

// diffState is one PR's fetched diff (or the fetch in flight / its error).
type diffState struct {
	text string
	err  error
	done bool
}

type diffMsg struct {
	url  string
	text string
	err  error
}

// maybeFetchDiff starts a diff fetch for the selected PR when the diff pane
// is showing and we have neither the diff nor a fetch in flight.
// fileState is the file list for a PR, created empty on first use so the
// pane can render "loading" before the fetch lands.
func (v *View) fileState(p pr) *filesState {
	if v.files == nil {
		v.files = map[string]*filesState{}
	}
	st, ok := v.files[p.URL]
	if !ok {
		st = &filesState{open: map[string]bool{}, reviewed: map[string]bool{}}
		v.files[p.URL] = st
	}
	return st
}

// filesHint names the keys the file list answers to, since they are not
// the list's own.
func (v *View) filesHint() string {
	if !v.PaneFocused() {
		return "→ to focus"
	}
	return "↑↓ move · +/→ expand · -/← collapse · space reviewed · esc back"
}

// maybeFetchFiles lists the selected PR's files once the pane is open.
func (v *View) maybeFetchFiles() tea.Cmd {
	if v.pane != paneFiles {
		return nil
	}
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	st := v.fileState(p)
	if st.done || st.err != nil {
		return nil
	}
	return fetchFiles(p.URL, p.repo(), p.Number)
}

// SetPreviewViewport is the root model's note of what the preview shows.
func (v *View) SetPreviewViewport(offset, rows int) { v.vpOffset, v.vpRows = offset, rows }

// fileRowVisible reports whether a file-list row was on screen in the last
// frame. With nothing known yet it says yes, so the keys are never trapped.
func (v *View) fileRowVisible(st *filesState, row int) bool {
	if row < 0 || row >= len(st.rowLine) || v.vpRows <= 0 {
		return true
	}
	line := v.paneHeader + st.rowLine[row]
	return line >= v.vpOffset && line < v.vpOffset+v.vpRows
}

// showFileRow asks the root model to scroll the selected file into view
// when it is not, a third of the way down so the lines under it show too.
func (v *View) showFileRow(st *filesState) {
	if st.sel < 0 || st.sel >= len(st.rowLine) || v.fileRowVisible(st, st.sel) {
		return
	}
	l := max(0, v.paneHeader+st.rowLine[st.sel]-v.vpRows/3)
	v.pendingJump = &l
}

func (v *View) jumpSize() int {
	if v.jump <= 0 {
		return ui.DefaultListJump
	}
	return v.jump
}

// updateFiles handles keys while the file list has the keys. Reports
// whether it consumed the key, like the jobs pane.
func (v *View) updateFiles(msg tea.KeyMsg) (tea.Cmd, bool) {
	st := v.fileState(v.list.Selected())
	rows := st.rows()
	if len(rows) == 0 {
		// Nothing to collapse, so left is straight to leaving the pane;
		// an empty list must not trap the keys.
		if msg.String() == "left" || msg.String() == "h" {
			if v.floatReveal {
				return v.leaveFloat(), true
			}
			v.paneFocus = false
			return nil, true
		}
		return nil, false
	}
	// The cursor sits on file rows only: patch lines are not targets, so
	// moving skips over whatever is expanded.
	fileAt := func(i int) int {
		for ; i >= 0 && i < len(rows); i++ {
			if rows[i].patch == "" {
				return i
			}
		}
		return -1
	}
	move := func(d int) {
		for i := st.sel + d; i >= 0 && i < len(rows); i += d {
			if rows[i].patch == "" {
				st.sel = i
				return
			}
		}
	}
	// Jump keys scroll the pane and never move the cursor: inside a diff
	// taller than the pane they are how you read it, and the files are
	// reached with the plain arrows.
	if key.Matches(msg, v.nav.JumpUp) || key.Matches(msg, v.nav.JumpDown) {
		d := v.jumpSize()
		if key.Matches(msg, v.nav.JumpUp) {
			d = -d
		}
		v.scrollBy += d
		return nil, true
	}
	// nextFile is the file row in direction d, or -1 at the end.
	nextFile := func(d int) int {
		for i := st.sel + d; i >= 0 && i < len(rows); i += d {
			if rows[i].patch == "" {
				return i
			}
		}
		return -1
	}
	// step is the arrows' contextual move: to the neighbouring file when
	// it is on screen, otherwise a line of scroll, so an expanded diff
	// taller than the pane can be read without the cursor leaving it.
	step := func(d int) {
		if t := nextFile(d); t >= 0 && v.fileRowVisible(st, t) {
			st.sel = t
			return
		}
		v.scrollBy += d
	}
	cur := fileAt(st.sel)
	if cur < 0 {
		cur = fileAt(0)
	}
	name := ""
	if cur >= 0 {
		name = st.files[rows[cur].file].Filename
	}

	switch msg.String() {
	case "up", "k":
		step(-1)
	case "down", "j":
		step(1)
	case "+", "right", "l":
		if name != "" && !st.open[name] {
			st.open[name] = true
			return nil, true
		}
		if msg.String() == "right" {
			return nil, false // nothing to expand: let the pane keep focus
		}
	case "-", "left", "h":
		if name != "" && st.open[name] {
			st.open[name] = false
			// Collapsed from partway down its hunk, the pane would be left
			// pointing at whatever moved up into that space; its own row
			// is the sensible place to be.
			v.showFileRow(st)
			return nil, true
		}
		if msg.String() == "left" || msg.String() == "h" {
			if v.floatReveal {
				return v.leaveFloat(), true
			}
			v.paneFocus = false
			return nil, true
		}
	case "space":
		// The GitHub UI's "viewed" checkbox: mark a file read and move on,
		// so working down a large PR is one key per file.
		if name != "" {
			st.reviewed[name] = !st.reviewed[name]
			if st.reviewed[name] {
				// Reviewed folds away, as on GitHub. The fold changes the
				// row layout, so find this file again before moving on, and
				// re-render so the row positions the follow uses are current.
				st.open[name] = false
				rows = st.rows()
				for i, r := range rows {
					if r.patch == "" && st.files[r.file].Filename == name {
						st.sel = i
						break
					}
				}
				move(1)
				v.PreviewView()
				v.showFileRow(st) // the next file may be below a tall diff
			}
		}
	default:
		return nil, false
	}
	return nil, true
}

func (v *View) maybeFetchDiff() tea.Cmd {
	if v.pane != paneDiff {
		return nil
	}
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	if _, started := v.diffs[p.URL]; started {
		return nil
	}
	if v.diffs == nil {
		v.diffs = map[string]diffState{}
	}
	v.diffs[p.URL] = diffState{} // in flight
	url, num, repo := p.URL, p.Number, p.repo()
	return func() tea.Msg {
		out, err := exec.Command("gh", "pr", "diff", strconv.Itoa(num), "-R", repo).Output()
		if err != nil {
			return diffMsg{url: url, err: cmdErr(err)}
		}
		return diffMsg{url: url, text: string(out)}
	}
}

// applySort rebuilds the list: own PRs sorted, then, when the toggle is on
// and there are any, the review-requested PRs, sorted the same way.
//
// Both sections get a labeled band, but only when there really are two of
// them. A lone section needs no header — it would cost two rows to tell the
// user what the whole screen already is — so a list that is all own PRs, or
// all review requests, renders flat exactly as it did before. The one
// exception is a failed review fetch, which has nowhere else to report
// itself and so keeps its band regardless.
func (v *View) applySort() {
	mine := sortPRs(v.raw, v.sort, v.rev)
	if v.reviewsOnly {
		mine = nil // the review search is the whole list, whatever raw holds
	}
	rev := sortPRs(v.reviewRaw, v.sort, v.rev)
	// An approved PR is waiting on its author, so hide it by default and
	// let the toggle bring it back: an approval from someone else does not
	// mean you are done with it, you may still want to comment.
	//
	// Approved-and-open only. A merged PR is gone from the list either way
	// with the default is:open filter, and a merged one is not something
	// this toggle should resurrect if that filter is widened.
	if v.hideApproved {
		kept := rev[:0]
		for _, p := range rev {
			if !p.approvedAndOpen() {
				kept = append(kept, p)
			}
		}
		rev = kept
	}
	// A draft is its author's business until it is marked ready. Both
	// sections: your own drafts clutter the list as much as other people's.
	if v.hideDrafts {
		mine, rev = dropDrafts(mine), dropDrafts(rev)
	}
	if v.cfg.MarkReviewed {
		for i := range rev {
			rev[i].Reviewed = rev[i].reviewedByMe()
		}
	}
	// Labels fill the space a hidden preview frees up, and only when the
	// list is wide enough that they are not crowding the metadata out.
	labels := !v.previewShown && v.listW >= labelColMinWidth
	for _, set := range [][]pr{mine, rev} {
		for i := range set {
			set[i].Unread = v.unreadOn && v.unread[set[i].URL]
			set[i].UnreadGutter = v.unreadOn
			set[i].ShowLabels = labels
		}
	}

	// Every section keeps its band, with the review section on or off and
	// in the Reviews tab alike: the band is what says which list this is
	// and which search produced it, and a header that comes and goes with
	// the toggle leaves a bare list to be puzzled out.
	var items []pr
	if !v.reviewsOnly {
		items = append(items, pr{Separator: v.bandWithQuery(
			sectionLabel("MY PULL REQUESTS", len(mine), 0, v.minePage.total),
			v.cfg.Filter)})
		items = append(items, v.groupSection(mine)...)
	}
	if v.showReview {
		items = append(items, pr{Separator: v.bandWithQuery(
			v.reviewLabel(rev), v.cfg.ReviewQuery())})
		items = append(items, v.groupSection(rev)...)
	}
	v.list.SetItems(items)
}

// departed says why the row you were on is gone after a refresh, so the
// cursor landing on another PR does not pass for the same one: the toast
// names the PR and the reason (approved and hidden, merged, dropped by
// the search), and the list is where it was.
func (v *View) departed(before pr, next []pr) tea.Cmd {
	if before.URL == "" || v.list.Any(matchURL(before.URL)) {
		return nil
	}
	why := "no longer in the search results"
	for _, p := range next {
		if p.URL != before.URL {
			continue
		}
		switch {
		case p.State == "MERGED":
			why = "merged"
		case p.State == "CLOSED":
			why = "closed"
		case v.hideApproved && p.approvedAndOpen():
			why = "approved, hidden by hide_approved"
		case v.hideDrafts && p.IsDraft:
			why = "a draft, hidden by hide_drafts"
		}
	}
	body := fmt.Sprintf("%s#%d: %s", before.repo(), before.Number, why)
	return func() tea.Msg { return ui.ToastMsg{Title: "Left the list", Body: body} }
}

func dropDrafts(prs []pr) []pr {
	kept := prs[:0]
	for _, p := range prs {
		if !p.IsDraft {
			kept = append(kept, p)
		}
	}
	return kept
}

// reviewLabel is the review section's band text: a count, plus how many of
// those the viewer has already reviewed (so the dimmed rows below are
// accounted for before you read them), or the fetch error if there was one.
func (v *View) reviewLabel(rev []pr) string {
	if v.reviewErr != nil {
		return "REVIEW REQUESTED  ·  fetch failed (ctrl+r)"
	}
	var reviewed int
	for _, p := range rev {
		if p.Reviewed {
			reviewed++
		}
	}
	return sectionLabel("REVIEW REQUESTED", len(rev), reviewed, v.reviewPage.total)
}

// sectionLabel formats a band label: an upper-case name (distinct from the
// Title Case swimlane headers nested under it) and a count.
// sectionLabel names a section and counts it. total is what the search
// matched: when more rows are still unpaged it reads "20 of 79", so a
// partially loaded list never looks like the whole set.
// bandWithQuery appends a section's search to its band. The label keeps
// its room and the query takes what is left: the counts are what the band
// is for, so the query is the part that gives way on a narrow terminal.
func (v *View) bandWithQuery(label, query string) string {
	q := strings.TrimSpace(query)
	// Editing this section's filter: the band shows what is being typed,
	// so the change appears on the row whose list it will change rather
	// than in a header far from it.
	if v.filterEd != nil && v.filterEd.label == bandSection(label) {
		q = v.filterEd.query + "█"
	}
	if q == "" || v.listW <= 0 {
		return label
	}
	// Glyph carries its own trailing space; adding another leaves the icon
	// with a gap after it and none before.
	icon := ui.Glyph(ui.IconSearch, "?")
	// The query sits right after the counts, so it reads as belonging to
	// this section rather than floating somewhere in the band. Faint and
	// italic sets it apart from the label without a second colour: the
	// band is reverse video, so the terminal blends the text toward the
	// accent behind it and that works whatever the theme's accent is.
	inner := v.listW - 2
	avail := inner - lipgloss.Width(label) - lipgloss.Width(icon) - bandGap
	if avail < bandQueryMin {
		return label // too little room to say anything useful
	}
	shown := ansi.Truncate(q, avail, "…")
	return label + strings.Repeat(" ", bandGap) +
		ui.Faint.Italic(true).Render(icon+shown)
}

// bandSection names which search a band belongs to, matching the editor's
// own label so the two can be paired.
func bandSection(label string) string {
	if strings.HasPrefix(label, "REVIEW REQUESTED") {
		return "review requests"
	}
	return "my PRs"
}

const (
	// bandQueryMin is the least room worth showing a query in: below it the
	// ellipsis says more than the text does.
	bandQueryMin = 12
	// bandGap is the least space between the counts and the query, so they
	// never run together when the query happens to fill the row.
	bandGap = 4
)

func sectionLabel(name string, n, reviewed, total int) string {
	label := fmt.Sprintf("%s  ·  %d", name, n)
	if total > n {
		label = fmt.Sprintf("%s  ·  %d of %d", name, n, total)
	}
	if reviewed > 0 {
		label += fmt.Sprintf("  ·  %d reviewed", reviewed)
	}
	return label
}

// groupSection inserts swimlane headers into one sorted section when
// grouping is on and the active sort declares a dimension.
func (v *View) groupSection(items []pr) []pr {
	if !v.grouping {
		return items
	}
	label := groupLabelFn(v.sort)
	if label == nil {
		return items
	}
	return ui.InsertGroups(items, label, func(l string) pr { return pr{Separator: l, Group: true} })
}

// markUnread records rows that were not in the previous set. It runs whether
// or not notifications or the marks themselves are on: the mark is how you
// catch up on what arrived while you were not looking, and the toggle only
// decides whether it is drawn.
func (v *View) markUnread(prev, next []pr, seeded bool) {
	// The first load of a section is everything, not "new": marking it
	// would light up the whole list on startup.
	if !seeded {
		return
	}
	known := make(map[string]bool, len(prev))
	for _, p := range prev {
		known[p.URL] = true
	}
	for _, p := range next {
		if !known[p.URL] {
			if v.unread == nil {
				v.unread = map[string]bool{}
			}
			v.unread[p.URL] = true
		}
	}
}

// clearUnread drops the mark for the selected row: looking at it is what
// makes it read.
func (v *View) clearUnread() { v.clearUnreadFor(v.list.Selected().URL) }

// clearUnreadFor reads one row by URL. The selection has already moved by
// the time a move is handled, so the caller that is leaving a row has to
// name it: clearing "the selection" there would read the row you land on.
func (v *View) clearUnreadFor(url string) {
	if url == "" || !v.unread[url] {
		return
	}
	delete(v.unread, url)
	v.applySort()
	v.saveCache()
	if p, ok := v.prByURL(url); v.unreadSync && ok && p.repo() != "" && p.Number != 0 {
		v.syncPending = append(v.syncPending, markThreadRead(p.repo(), p.Number))
	}
}

// drainSync returns the write-backs clearUnread queued, if any.
func (v *View) drainSync() tea.Cmd {
	if len(v.syncPending) == 0 {
		return nil
	}
	cmds := v.syncPending
	v.syncPending = nil
	return tea.Batch(cmds...)
}

// saveCache rewrites the cache so a mark cleared (or earned) in this session
// survives a restart. Cheap: one small JSON file, written atomically.
func (v *View) saveCache() {
	mine := v.raw
	if v.reviewsOnly {
		mine = nil
	}
	_ = cache.Save(v.cacheName(), cachedPRs{Mine: mine, Review: v.reviewRaw, Unread: v.unreadURLs()})
}

// notifyNewReviews posts a notification for review requests that appeared
// since the last fetch (nil while unseeded or when notifications are off).
func (v *View) notifyNewReviews(prev, next []pr) tea.Cmd {
	if v.notifier == nil || !v.seeded {
		return nil
	}
	known := make(map[string]bool, len(prev))
	for _, p := range prev {
		known[p.URL] = true
	}
	var fresh []pr
	for _, p := range next {
		if !known[p.URL] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	title := "PR needs your review"
	if len(fresh) > 1 {
		title = fmt.Sprintf("%d PRs need your review", len(fresh))
	}
	var lines []string
	for _, p := range fresh {
		lines = append(lines, fmt.Sprintf("%s#%d: %s (@%s)", p.repo(), p.Number, p.Title, p.Author.Login))
	}
	body := strings.Join(lines, "\n")
	// One new PR opens that PR; several open the review queue, since there
	// is no single right destination.
	url := "https://github.com/pulls/review-requested"
	if len(fresh) == 1 {
		url = fresh[0].URL
	}
	n := v.notifier
	return func() tea.Msg { return n.Notify(title, body, url) }
}

// ScrollList moves the list selection by n rows (mouse wheel).
func (v *View) ScrollList(n int) tea.Cmd {
	before := v.list.Selected().URL
	v.list.ScrollBy(n)
	return v.mouseMoved(before)
}

// ClickList selects the row under a click in the list column; y is relative
// to the column top, whose first line is the header.
func (v *View) ClickList(_, y int) (bool, tea.Cmd) {
	v.jobsFocus = false // a click in the list is back in the list
	before := v.list.Selected().URL
	if !v.list.ClickAt(y - 1) {
		return false, nil
	}
	return true, v.mouseMoved(before)
}

// Activate runs the open action on the selection (a double-click).
func (v *View) Activate() tea.Cmd { return v.openSelected() }

// mouseMoved follows a mouse-driven selection change the way a j/k move does:
// restart the annotation cycle, fetch the pane's data once the selection
// settles, and end a transient preview reveal.
func (v *View) mouseMoved(before string) tea.Cmd {
	// Paging first: scrolling to the end with the wheel loads the next page
	// even when the selection did not move (already on the last row).
	var more tea.Cmd
	if v.list.AtEnd() {
		more = v.fetchMore()
	}
	if v.list.Selected().URL == before {
		return more
	}
	v.annIdx = 0
	v.resetToggles()
	if v.previewShown {
		v.clearUnreadFor(before)
		if !v.floatReveal {
			v.clearUnread()
		}
	}
	return tea.Batch(v.scheduleSettle(), v.scheduleRowRefresh(), ui.ConcealPreview, more, v.drainSync())
}

func (v *View) SetSize(listW, prevW, h int) {
	was := v.listW
	v.listW, v.prevW, v.height = listW, prevW, h
	v.list.SetSize(listW, max(1, h-1)) // reserve a row for the header line
	v.bodyKey = ""                     // width changed: invalidate the body cache
	// The label column depends on the list's width, so a resize across the
	// threshold has to re-decide it.
	if was != listW && v.seeded {
		v.applySort()
	}
}

func (v *View) ListView() string {
	// The list dims while a pane has the keys, so the only lit cursor on
	// screen is the one the arrows will move.
	v.list.SetBlurred(v.PaneFocused())
	header := ""
	switch {
	case v.input != nil:
		header = v.threadPromptLine()
	default:
		header = v.list.FilterLine()
	}
	if header == "" {
		header = ui.Faint.Render(v.statusText())
	}
	return header + "\n" + v.list.View()
}

func (v *View) statusText() string {
	switch {
	case v.loading:
		return "Loading PRs…"
	case v.err != nil:
		return "Error (ctrl+r to retry)"
	case v.flash != "":
		return v.flash
	case v.reviewsOnly:
		if v.reviewLoading && len(v.reviewRaw) == 0 {
			return "Loading review requests…"
		}
		s := fmt.Sprintf("%d to review", len(v.reviewRaw))
		if v.reviewPage.total > len(v.reviewRaw) {
			s = fmt.Sprintf("%d of %d to review", len(v.reviewRaw), v.reviewPage.total)
		}
		return fmt.Sprintf("%s · sort: %s%s", s, sortName[v.sort], ui.RevMarker(v.rev))
	default:
		s := fmt.Sprintf("%d PRs", len(v.raw))
		if v.minePage.total > len(v.raw) {
			s = fmt.Sprintf("%d of %d PRs", len(v.raw), v.minePage.total)
		}
		if v.showReview && len(v.reviewRaw) > 0 {
			// Same shape as the Reviews tab's own line, so the two read alike.
			if v.reviewPage.total > len(v.reviewRaw) {
				s += fmt.Sprintf(" · %d of %d to review", len(v.reviewRaw), v.reviewPage.total)
			} else {
				s += fmt.Sprintf(" · %d to review", len(v.reviewRaw))
			}
		}
		return fmt.Sprintf("%s · sort: %s%s", s, sortName[v.sort], ui.RevMarker(v.rev))
	}
}

func (v *View) PreviewView() string {
	if v.err != nil {
		return ui.Red.Width(v.prevW).Render(v.err.Error())
	}
	p := v.list.Selected()
	if p.URL == "" {
		return ui.Faint.Render("No PR selected.")
	}

	var b strings.Builder
	// The same order as the list row: metadata over the title.
	b.WriteString(ui.Dim.Render(fmt.Sprintf("%s #%d  ·  @%s  ·  %s ago",
		p.repo(), p.Number, p.Author.Login, ui.Age(p.UpdatedAt))))
	b.WriteString("\n")
	b.WriteString(ui.Bold.Width(v.prevW).Render(p.Title))
	b.WriteString("\n\n")

	// What the PR is: state, where it is going, whether it can get there.
	status := p.stateIcon() + " " + stateWord(p)
	if p.BaseRefName != "" && p.HeadRefName != "" {
		status += ui.Dim.Render("  ·  ") + ui.Faint.Italic(true).Render(p.BaseRefName+" ← "+p.HeadRefName)
	}
	if p.Mergeable == "CONFLICTING" {
		status += ui.Dim.Render("  ·  ") + ui.Red.Render("⚠ conflicts")
	}
	b.WriteString(status)
	b.WriteString("\n")
	// How it is doing: checks and review, with the diff and comment counts
	// at the right edge.
	health := fmt.Sprintf("%s %s   %s %s", p.ciIcon(), ciWord(p), p.reviewIcon(), reviewWord(p))
	var counts []string
	if d := p.diffCell(); d != "" {
		counts = append(counts, d)
	}
	if c := p.commentsCell(); c != "" {
		counts = append(counts, c)
	}
	b.WriteString(rightAligned(health, strings.Join(counts, "  "), v.prevW))
	b.WriteString("\n")

	if pills := labelPills(p.Labels.Nodes); pills != "" {
		b.WriteByte('\n')
		b.WriteString(pills)
		b.WriteByte('\n')
	}

	b.WriteString(ui.Dim.Render(strings.Repeat("─", min(v.prevW, 60))))
	b.WriteString("\n")
	// Jump anchors are body-relative; remember how many header lines sit
	// above the body so jumps land on the right rendered line.
	v.paneHeader = strings.Count(b.String(), "\n")
	switch v.pane {
	case paneDiff:
		b.WriteString(v.renderedDiff(p))
	case paneComments:
		b.WriteString(v.renderedComments(p))
	case paneJobs:
		b.WriteString(v.renderedJobs(p))
	case paneFiles:
		b.WriteString(renderFilesPane(v.fileState(p), v.prevW,
			v.PaneFocused(), v.filesHint(), v.threadsFor(p)))
	default:
		// Description, then checks, then comments: the summary reads top to
		// bottom in the order you want it, with the detail panes (diff,
		// comments) staying bare because they are already the detail.
		b.WriteString(ui.BlockHeader("Description"))
		b.WriteString("\n")
		b.WriteString(v.renderedBody(p))
		if blk := v.checksBlock(p); blk != "" {
			b.WriteString("\n\n")
			b.WriteString(ui.BlockHeader("Checks"))
			b.WriteString("\n")
			b.WriteString(blk)
			if _, _, _, total := p.checkCounts(); total > 0 {
				b.WriteString("\n" + ui.Faint.Render(fmt.Sprintf("  %s or click %s",
					v.keyHint("jobs"), jobsMarker)))
			}
		}
		b.WriteString("\n\n")
		b.WriteString(v.commentsBlock(p))
		if blk := v.linearBlock(p); blk != "" {
			b.WriteString("\n\n")
			b.WriteString(blk)
		}
	}
	return b.String()
}

// linearBlock names the Linear issue(s) the PR references, with the
// issue's title where the Linear view has it, and how to get there.
func (v *View) linearBlock(p pr) string {
	ids := p.linearRefs()
	if len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(ui.BlockHeader("Linear"))
	for _, id := range ids {
		line := "  " + ui.Cyan.Render(id)
		if v.store != nil {
			if iss, ok := v.store.Issue(id); ok && iss.Title != "" {
				line += "  " + ui.Truncate(iss.Title, max(10, v.prevW-lipgloss.Width(line)-2))
			}
		}
		b.WriteString("\n" + line)
	}
	b.WriteString("\n" + ui.Faint.Render("  l to jump to ticket"))
	return b.String()
}

// blockHeader labels a preview section. One style for all of them, so the
// pane reads as a list of sections rather than three unrelated widgets.
// reviewers are the pending review requests as mentions: @login for a
// person, @org/team for a team.
func (p pr) reviewers() []string {
	var out []string
	for _, n := range p.ReviewRequests.Nodes {
		switch r := n.RequestedReviewer; {
		case r.Login != "":
			out = append(out, "@"+r.Login)
		case r.CombinedSlug != "":
			out = append(out, "@"+r.CombinedSlug)
		}
	}
	return out
}

// checksBlock is the bordered CI summary: what is blocking the merge, and how
// the checks are doing. Bordered in the colour of the worst state, so a
// glance at the frame says whether anything needs attention.
func (v *View) checksBlock(p pr) string {
	var rows []string
	switch p.ReviewDecision {
	case "APPROVED":
		rows = append(rows, ui.Green.Render(ui.IconApproved+" Approved"))
	case "CHANGES_REQUESTED":
		rows = append(rows, ui.Red.Render(ui.IconChanges+" Changes requested"))
	case "REVIEW_REQUIRED":
		waiting := "waiting on a reviewer"
		if who := p.reviewers(); len(who) > 0 {
			waiting = "waiting on " + strings.Join(who, ", ")
		}
		rows = append(rows, ui.Yellow.Render(ui.IconReviewReq+" Review required")+
			"\n"+ui.Dim.Render("  "+waiting))
	}

	pass, fail, run, total := p.checkCounts()
	switch {
	case total == 0 && p.ciState() == "":
		// No checks at all: say nothing rather than an empty row.
	case fail > 0:
		rows = append(rows, ui.Red.Render(fmt.Sprintf("%s %d of %d checks failed", ui.IconCIFail, fail, total))+
			"\n"+ui.Dim.Render(fmt.Sprintf("  %d passed, %d running", pass, run)))
	case run > 0:
		rows = append(rows, ui.Yellow.Render(fmt.Sprintf("%s %d checks running", ui.IconCIPending, run))+
			"\n"+ui.Dim.Render(fmt.Sprintf("  %d of %d passed so far", pass, total)))
	case pass > 0:
		rows = append(rows, ui.Green.Render(ui.IconCIOK+" All checks have passed")+
			"\n"+ui.Dim.Render(fmt.Sprintf("  %d successful", pass)))
	}

	if p.Mergeable == "CONFLICTING" {
		rows = append(rows, ui.Red.Render("⚠ Merging is blocked")+
			"\n"+ui.Dim.Render("  branch has conflicts"))
	}
	if len(rows) == 0 {
		return ""
	}

	border := ui.Pal().Green
	switch {
	case fail > 0 || p.Mergeable == "CONFLICTING" || p.ReviewDecision == "CHANGES_REQUESTED":
		border = ui.Pal().Red
	case run > 0 || p.ReviewDecision == "REVIEW_REQUIRED":
		border = ui.Pal().Yellow
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Padding(0, 1).
		Width(max(20, min(v.prevW, 60)) - 4).
		Render(strings.Join(rows, "\n\n"))
}

// commentsBlock closes the summary with whether there is a conversation to
// read, and which key opens it.
func (v *View) commentsBlock(p pr) string {
	head := ui.BlockHeader("Comments")
	if p.Comments.TotalCount == 0 {
		return head + "\n" + ui.Faint.Render("  none yet")
	}
	return head + "\n" + ui.Faint.Render(fmt.Sprintf("  %d · %s or click %s",
		p.Comments.TotalCount, v.keyHint("comments"), commentsMarker))
}

// renderedDiff renders the diff pane body for p (the colorized diff with
// inline review threads pinned in), memoized per (PR, width, comments
// revision, palette). It refreshes v.anchors as a side effect so the
// thread-jump keys have targets.
func (v *View) renderedDiff(p pr) string {
	d, ok := v.diffs[p.URL]
	switch {
	case !ok || !d.done:
		return ui.Faint.Render("Loading diff…")
	case d.err != nil:
		return ui.Red.Render(d.err.Error())
	case strings.TrimSpace(d.text) == "":
		return ui.Faint.Render("(empty diff)")
	}

	key := fmt.Sprintf("diff:%s:%d:%d:%d", p.URL, v.prevW, v.commentsRev, ui.PaletteGen())
	if v.paneKey == key {
		v.anchors = v.paneAnchors
		return v.paneText
	}
	var anns []ui.DiffAnnotation
	if st, ok := v.comments[p.URL]; ok && st.done && st.err == nil {
		anns = threadAnnotations(st.data.ReviewThreads.Nodes, v.prevW)
	}
	text, anchors := ui.RenderAnnotatedDiff(d.text, v.prevW, anns)
	v.paneKey, v.paneText, v.paneAnchors = key, text, anchors
	v.anchors = anchors
	return text
}

// renderedComments renders the comments pane for p, memoized like the diff.
func (v *View) renderedComments(p pr) string {
	st, ok := v.comments[p.URL]
	switch {
	case !ok || !st.done:
		return ui.Faint.Render("Loading comments…")
	case st.err != nil:
		return ui.Red.Render(st.err.Error())
	}

	key := fmt.Sprintf("comments:%s:%d:%d:%d", p.URL, v.prevW, v.commentsRev, ui.PaletteGen())
	if v.paneKey == key {
		v.anchors = v.paneAnchors
		return v.paneText
	}
	text, anchors := renderCommentsPane(st.data, v.prevW)
	v.paneKey, v.paneText, v.paneAnchors = key, text, anchors
	v.anchors = anchors
	return text
}

// renderedBody returns the glamour-rendered PR body, memoized per (PR, width).
func (v *View) renderedBody(p pr) string {
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return ui.Faint.Render("(no description)")
	}
	expanded := v.expanded == p.URL
	key := fmt.Sprintf("%d:%d:%d:%t", p.Number, v.prevW, ui.PaletteGen(), expanded)
	if v.bodyKey == key {
		return v.body
	}
	out := ui.Markdown(body, v.prevW)
	switch limit := v.cfg.SummaryLines; {
	case limit <= 0:
		// Truncation off: the whole description, no hint.
	case expanded:
		out += "\n" + ui.Faint.Render(fmt.Sprintf("… %s or click %s",
			v.keyHint("expand"), expandMarker))
	default:
		out = truncateSummary(out, limit, v.keyHint("expand"))
	}
	v.bodyKey, v.body = key, out
	return out
}

// hintMarkers are the phrases the clickable preview hints end with. A
// click is matched against the rendered text rather than a tracked line
// number, so a layout change cannot silently move the target.
const (
	expandMarker   = "to toggle the description"
	commentsMarker = "to toggle comments"
	jobsMarker     = "to show jobs"
)

// ClickPreview toggles whatever hint the click landed on. line is counted
// from the top of the rendered preview, so it survives scrolling; col is
// unused for now, the hints span their whole line.
func (v *View) ClickPreview(line, col int) tea.Cmd {
	lines := strings.Split(v.PreviewView(), "\n")
	if line < 0 || line >= len(lines) {
		return nil
	}
	// The jobs pane is one you work in, so a click never puts it away. On
	// a row it focuses the pane and moves the cursor there (a second click
	// does what enter would, short of launching a pager from the mouse);
	// anywhere else, including an open log, it just takes the keys.
	if v.pane == paneJobs {
		if v.logView == nil {
			if cmd, ok := v.clickJobRow(line); ok {
				return cmd
			}
		}
		v.jobsFocus = true
		return nil
	}
	text := ansi.Strip(lines[line])
	switch {
	// A click anywhere in a diff or comments pane puts it away, the same
	// as pressing the key again: hunting for the hint to close what you
	// opened is busywork.
	case v.pane == paneDiff || v.pane == paneComments:
		return v.setPane(v.pane)
	case strings.Contains(text, expandMarker):
		if sel := v.list.Selected(); sel.URL != "" {
			if v.expanded == sel.URL {
				v.expanded = ""
			} else {
				v.expanded = sel.URL
			}
			v.bodyKey = ""
		}
		return nil
	case strings.Contains(text, commentsMarker):
		return v.setPane(paneComments)
	case strings.Contains(text, jobsMarker):
		return v.setPane(paneJobs)
	}
	return nil
}

// rightAligned puts right at the far edge of a width-wide line after left,
// or on the next line when the two do not fit side by side.
func rightAligned(left, right string, width int) string {
	if right == "" {
		return left
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left + "\n" + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// truncateSummary clips a rendered description to limit lines and says how to
// see the rest. A description that already fits is left alone, so the hint
// only appears when something is actually hidden.
func truncateSummary(rendered string, limit int, hintKey string) string {
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) <= limit {
		return rendered
	}
	kept := strings.Join(lines[:limit], "\n")
	hint := fmt.Sprintf("… %d more lines · %s or click %s",
		len(lines)-limit, hintKey, expandMarker)
	return kept + "\n\n" + ui.Faint.Render(hint)
}

// keyHint is the first key bound to an action, for a prompt telling the user
// which key does the thing.
func (v *View) keyHint(action string) string {
	if keys := v.keys.binding(action).Keys(); len(keys) > 0 {
		return keys[0]
	}
	return "?"
}

func (v *View) Bindings() []key.Binding {
	if v.jobsFocus && v.pane == paneJobs {
		if v.logView != nil {
			return v.logBindings()
		}
		return v.jobsBindings()
	}
	if v.PaneFocused() && v.pane == paneFiles {
		return v.filesBindings()
	}
	if v.reviewsOnly || v.reviewsElsewhere {
		return []key.Binding{v.keys.Open, v.keys.Diff, v.keys.Comments, v.keys.Jobs, v.keys.Start, v.keys.Copy, v.keys.Sort, v.keys.Rev, v.keys.HideDrafts, v.keys.EditFilter}
	}
	return []key.Binding{v.keys.Open, v.keys.Diff, v.keys.Comments, v.keys.Jobs, v.keys.Start, v.keys.Copy, v.keys.Sort, v.keys.Rev, v.keys.Review, v.keys.HideDrafts, v.keys.EditFilter}
}

// Status is the footer's right-hand slot. The list header already carries
// the counts and sort, so repeating them there just says everything twice;
// only a flash (an action's result) belongs in the footer.
func (v *View) Status() string {
	if v.flash != "" {
		return v.flash
	}
	return ""
}

// PreviewFocus implements the root model's focus hook: what the right pane
// is called while it has the keys ("jobs", or "log" with one open), "" while
// they are with the PR list.
func (v *View) PreviewFocus() string {
	switch {
	case v.pane != paneJobs || !v.jobsFocus:
		return ""
	case v.logView != nil:
		return "log"
	}
	return "jobs"
}

// FocusKeepLines implements the root model's focus hook: the list lines to
// leave lit while the pane has the keys, which are the PR whose jobs it is
// showing. The list sits under a one-line header in ListView.
func (v *View) FocusKeepLines() (first, n int, ok bool) {
	first, n, ok = v.list.SelectedLines()
	return first + 1, n, ok
}

// restoreFilter puts a rejected filter back, so the list is still the one
// the user had and the editor still opens on something that works.
func (v *View) restoreFilter(path, prev string) {
	if path == "github.filter" {
		v.cfg.Filter = prev
		return
	}
	v.cfg.ReviewFilter = prev
}

// filesBindings are the footer's keys while the file list has focus: its
// own, since the list's keys do nothing there.
func (v *View) filesBindings() []key.Binding {
	return []key.Binding{
		ui.Bind([]string{"+"}, "", "expand"),
		ui.Bind([]string{"-"}, "", "collapse"),
		ui.Bind([]string{"space"}, "", "reviewed"),
		ui.Bind([]string{"esc"}, "", "back"),
	}
}

// PaneFocused reports whether the preview pane has the keys.
func (v *View) PaneFocused() bool {
	// A float has the keys whatever it holds: the list is behind it, so
	// there is nothing else they could belong to. floatReveal, not
	// previewShown: with hide_preview on there is no float at all until
	// something opens one, and dimming the list before that is dimming it
	// for nothing. Esc still steps focus out of a float that stays open.
	if v.floatReveal {
		return v.paneFocus || v.jobsFocus
	}
	if !v.previewShown {
		return false
	}
	// Beside a visible list, focus is tied to a pane being open rather
	// than to the flag alone: a pane closed by any route (a review
	// submitted, a toggle reset) cannot leave the list dimmed with
	// nothing focused.
	if v.pane == paneBody {
		return false
	}
	return (v.jobsFocus && v.pane == paneJobs) || v.paneFocus
}

// FocusPane gives the pane the keys or takes them back, reporting whether
// anything changed so a no-op key does not redraw.
func (v *View) FocusPane(on bool) bool {
	if v.PaneFocused() == on {
		return false
	}
	switch v.pane {
	case paneJobs:
		v.jobsFocus = on
	case paneFiles:
		v.paneFocus = on
	case paneBody:
		// Floated, the description takes the keys to scroll it; beside a
		// visible list there is nothing to focus.
		if v.previewShown {
			return false
		}
		v.paneFocus = on
	default:
		v.paneFocus = on
	}
	return true
}

// PaneScrolls reports a pane that scrolls rather than holding its own
// cursor, so the arrows should move the preview.
func (v *View) PaneScrolls() bool {
	return v.PaneFocused() && v.pane != paneJobs && v.pane != paneFiles
}

// Dismiss closes the innermost pane this view has open, reporting whether
// it closed anything so the root model knows if esc still has work to do.
// Order matters beside a list: the log sits inside the jobs pane, which
// sits inside the preview, so esc walks out one layer at a time rather
// than collapsing everything at once.
func (v *View) Dismiss() bool {
	// Floated, esc closes the whole float whatever level it is on, a job
	// log included; the arrows are what step a level at a time. Reporting
	// nothing left to do hands the close to the root model, which owns the
	// float.
	if v.floatReveal {
		v.closeFloat()
		return false
	}
	if v.logView != nil {
		// Back to the jobs list, which is what the log's own hint says.
		v.logView = nil
		return true
	}
	switch {
	case v.PaneFocused():
		// Focus back to the list: the pane stays open beside it.
		v.jobsFocus, v.paneFocus = false, false
		return true
	case v.pane != paneBody:
		v.pane = paneBody
		return true
	}
	return false
}

func (v *View) InputActive() bool {
	return v.list.Filtering() || v.review != nil || v.rerun != nil ||
		v.input != nil || v.filterEd != nil
}

func (v *View) Fields() []string { return v.list.FieldNames() }

func (v *View) FilterState() (string, []string, bool) {
	return v.list.Query(), v.list.EnabledFields(), v.list.CaseSensitive()
}

func (v *View) SetFilter(query string, enabled []string, caseSensitive bool) {
	v.list.SetEnabledFields(enabled)
	v.list.SetCaseSensitive(caseSensitive)
	v.list.SetQuery(query)
}

// PreviewKey includes the pane mode so toggling description/diff resets the
// preview scroll.
func (v *View) PreviewKey() string {
	k := v.list.Selected().URL
	switch v.pane {
	case paneDiff:
		k += "#diff"
	case paneComments:
		k += "#comments"
	case paneJobs:
		k += "#jobs" + v.logKey()
	}
	return k
}

// --- preview text helpers ---------------------------------------------------

func stateWord(p pr) string {
	switch {
	case p.IsDraft:
		return ui.Dim.Render("draft")
	case p.State == "MERGED":
		return ui.Magenta.Render("merged")
	case p.State == "CLOSED":
		return ui.Red.Render("closed")
	default:
		return ui.Green.Render("open")
	}
}

func ciWord(p pr) string {
	switch p.ciState() {
	case "SUCCESS":
		return ui.Green.Render("checks passing")
	case "FAILURE", "ERROR":
		return ui.Red.Render("checks failing")
	case "PENDING", "EXPECTED":
		return ui.Yellow.Render("checks running")
	default:
		return ui.Dim.Render("no checks")
	}
}

func reviewWord(p pr) string {
	switch p.ReviewDecision {
	case "APPROVED":
		return ui.Green.Render("approved")
	case "CHANGES_REQUESTED":
		return ui.Red.Render("changes requested")
	case "REVIEW_REQUIRED":
		return ui.Yellow.Render("review required")
	default:
		return ui.Dim.Render("no review")
	}
}

func labelPills(labels []label) string {
	if len(labels) == 0 {
		return ""
	}
	pills := make([]string, 0, len(labels))
	for _, l := range labels {
		style := lipgloss.NewStyle().Padding(0, 1)
		if len(l.Color) != 6 {
			pills = append(pills, style.Render(l.Name))
			continue
		}
		c := lipgloss.Color("#" + l.Color)
		body := style.Background(c).Foreground(contrastFg(l.Color)).Render(l.Name)
		// Powerline half-circles in the label colour round the ends; without
		// decorative glyphs the plain padded block is the fallback.
		if ui.GlyphsOn() {
			cap := lipgloss.NewStyle().Foreground(c)
			body = cap.Render(ui.IconPillLeft) + body + cap.Render(ui.IconPillRight)
		}
		pills = append(pills, body)
	}
	return strings.Join(pills, " ")
}

// contrastFg picks black or white text for a hex background by luminance.
func contrastFg(hex string) color.Color {
	if len(hex) != 6 {
		return lipgloss.Color("15")
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 0)
	g, _ := strconv.ParseInt(hex[2:4], 16, 0)
	bl, _ := strconv.ParseInt(hex[4:6], 16, 0)
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
	if lum > 140 {
		return lipgloss.Color("0")
	}
	return lipgloss.Color("15")
}

// cmdErr unwraps *exec.ExitError to surface stderr in the message.
// statusCmd raises a fetch problem as an app-level message: the first line is
// the summary, the rest is detail for the log overlay.
func statusCmd(sev ui.Severity, err error) tea.Cmd {
	summary, detail, _ := strings.Cut(err.Error(), "\n")
	return func() tea.Msg { return ui.Status(sev, "PRs", summary, detail) }
}

func cmdErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}
