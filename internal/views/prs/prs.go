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
func sortByName(name string) (sortMode, bool) {
	for mode, n := range sortName {
		if n == name {
			return mode, true
		}
	}
	return sortRecent, false
}

// SortNames lists the sorts this view accepts, for the settings overlay.
func SortNames() []string {
	out := make([]string, 0, len(sortOrder))
	for _, mode := range sortOrder {
		out = append(out, sortName[mode])
	}
	return out
}

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
	page searchPage
	err  error
	// more marks a page fetched after the first, appended rather than
	// replacing what is already on screen.
	more bool
}

type reviewListMsg struct {
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
	grouping   bool // swimlanes derived from the active sort
	sort       sortMode
	rev        bool // sort order reversed
	store      *store.Store

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
	// floatReveal distinguishes a float, which closes when you move on,
	// from a pane that stays open and shows the row you arrive at.
	floatReveal bool

	// rowRefresh re-reads the selected PR once the cursor settles; rowGen
	// supersedes earlier ticks so cycling a list costs one request.
	rowRefresh bool
	rowGen     int

	// hideApproved drops already-approved PRs from the review list.
	hideApproved bool

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
	input  *threadFlow
	flash  string

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
type settleMsg struct{ gen int }

// scheduleSettle arms the debounce while a data pane is showing.
func (v *View) scheduleSettle() tea.Cmd {
	if v.pane == paneBody {
		return nil
	}
	v.settleGen++
	gen := v.settleGen
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return settleMsg{gen: gen} })
}

// paneMode selects the right pane's content for the selected PR.
type paneMode int

const (
	paneBody paneMode = iota
	paneDiff
	paneComments
	paneJobs
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
	bind := func(action, desc string, def ...string) key.Binding {
		return ui.Bind(km.Of("prs", action, def...), "", desc)
	}
	v := &View{
		cfg:        cfg,
		store:      st,
		notifier:   n,
		list:       ui.NewList[pr](),
		loading:    true,
		showReview: cfg.ShowReviewRequested != nil && *cfg.ShowReviewRequested,
		keys: viewKeys{
			Open:       bind("open", "open", "enter"),
			Copy:       bind("copy_url", "copy url", "y"),
			Diff:       bind("diff", "diff", "d"),
			Sort:       bind("sort", "sort", "s"),
			Rev:        bind("reverse", "reverse", "S"),
			Review:     bind("toggle_review", "review reqs", "w"),
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
		},
	}
	if mode, ok := sortByName(cfg.Sort); ok {
		v.sort = mode
	}
	v.rev = cfg.Reverse
	v.rowRefresh = cfg.RefreshRowEnabled()
	v.hideApproved = cfg.HideApproved
	v.list.SetRowHeight(2) // two-line rows: metadata + title
	v.list.Rebind(func(a string, d ...string) []string { return km.Of("list", a, d...) })
	v.nav = newNavKeys(km)

	// Paint last run's PRs immediately; the live fetch refreshes them.
	if cached, ok := cache.Load[cachedPRs](cacheName); ok && len(cached.Mine)+len(cached.Review) > 0 {
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

const cacheName = "prs"

func (v *View) Title() string { return "PRs" }

func (v *View) Init() tea.Cmd {
	v.loading = true
	// A refresh covers the jobs pane too: it is the retry for a fetch that
	// failed, and otherwise refetches what is on screen.
	if st, ok := v.jobs[v.list.Selected().URL]; ok && !st.inFlight {
		st.fetchedAt, st.failures = time.Time{}, 0
	}
	return tea.Batch(v.fetch(), v.maybeFetchJobs())
}

func (v *View) Loading() bool { return v.loading || v.reviewLoading }

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
  number title url state isDraft updatedAt headRefName
  additions deletions mergeable reviewDecision body
  viewerLatestReview { state }
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
	q := ensurePR(v.cfg.Filter)
	size := v.pageSize()
	cmds := []tea.Cmd{func() tea.Msg {
		page, err, hidden := searchPRs(q, size, "")
		if err == nil {
			err = hidden
		}
		return mineMsg{page: page, err: err}
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
	rq := ensurePR(v.cfg.ReviewFilter)
	size := v.pageSize()
	v.reviewLoading = true
	return func() tea.Msg {
		page, err, hidden := searchPRs(rq, size, "")
		if err == nil {
			err = hidden
		}
		return reviewListMsg{page: page, err: err}
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
		rq, cursor := ensurePR(v.cfg.ReviewFilter), v.reviewPage.cursor
		return func() tea.Msg {
			page, err, hidden := searchPRs(rq, size, cursor)
			if err == nil {
				err = hidden
			}
			return reviewListMsg{page: page, err: err, more: true}
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
			return mineMsg{page: page, err: err, more: true}
		}
	}
	return nil
}

func (v *View) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case mineMsg:
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
		_ = cache.Save(cacheName, cachedPRs{Mine: v.raw, Review: v.reviewRaw, Unread: v.unreadURLs()})
		return partial
	case reviewListMsg:
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
		_ = cache.Save(cacheName, cachedPRs{Mine: v.raw, Review: next, Unread: v.unreadURLs()})
		return cmd
	case diffMsg:
		v.diffs[msg.url] = diffState{text: msg.text, err: msg.err, done: true}
		return nil
	case settleMsg:
		if msg.gen != v.settleGen {
			return nil // superseded by further navigation
		}
		return tea.Batch(v.maybeFetchDiff(), v.maybeFetchComments(), v.maybeFetchJobs(), v.maybeFetchLogs())
	case logMsg:
		return v.applyLog(msg)
	case jobsMsg:
		return v.applyJobs(msg)
	case jobsTickMsg:
		// Superseded, or the cursor moved on: the watch follows the PR in
		// front of you and stops when you leave it.
		if msg.gen != v.jobsGen || msg.url != v.list.Selected().URL {
			return nil
		}
		return v.maybeFetchJobs()
	case rerunDoneMsg:
		return v.applyRerun(msg)
	case rowSettleMsg:
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
		v.applySort()
		v.bodyKey = "" // checks and review state render in the preview
		return nil
	case mergeDoneMsg:
		v.review = nil
		if msg.err != nil {
			v.flash = ui.Red.Render("merge failed: " + msg.err.Error())
			return statusCmd(ui.SeverityError, msg.err)
		}
		v.flash = ui.Green.Render("✓ " + msg.what)
		// A merged PR leaves the search on the next fetch; auto-merge
		// leaves it open, so only refetch and let the row speak for itself.
		v.resetToggles()
		return tea.Batch(ui.ConcealPreview, v.fetch())
	case reviewDoneMsg:
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
	case ui.UnreadMsg:
		v.unreadOn = bool(msg)
		if !v.unreadOn {
			v.unread = nil
		}
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
		// A hidden pane cannot hold the keys: j/k would move a cursor you
		// cannot see.
		if !v.previewShown {
			v.jobsFocus, v.logView = false, nil
		}
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
		return nil
	case ui.GroupingMsg:
		v.grouping = bool(msg)
		v.applySort()
		return nil
	case threadDoneMsg:
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
			// through less. github.diff_pane opts into the in-pane diff.
			if !v.cfg.DiffPane {
				return v.diffInPager()
			}
			return v.setPane(paneDiff)
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
				return toggleResolve(v.list.Selected().URL, t.ID, t.IsResolved)
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
				// Reviewing reads better against the diff, where enabled.
				if v.cfg.DiffPane && v.pane == paneBody {
					v.pane = paneDiff
				}
				return tea.Batch(v.maybeFetchDiff(), v.maybeFetchComments())
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
		if v.previewShown {
			return nil
		}
		return ui.ConcealPreview
	}
	v.pane = mode
	v.annIdx = 0
	// Opening the jobs pane is asking to look through the jobs, so it takes
	// the keys straight away; esc hands them back to the PR list.
	v.jobsFocus = mode == paneJobs
	// The pane is about to show a diff, comments or jobs; a hidden preview
	// would swallow it silently.
	return tea.Batch(ui.RevealPreview, v.maybeFetchDiff(), v.maybeFetchComments(), v.maybeFetchJobs(), v.maybeFetchLogs())
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
		// Show the diff and get out of the way; 'r' reopens the popup.
		v.review = nil
		if v.cfg.DiffPane {
			v.pane = paneDiff
			return tea.Batch(ui.RevealPreview, v.maybeFetchDiff(), v.maybeFetchComments())
		}
		return v.diffInPager()
	case mergeLabel:
		v.askMerge("merge")
	case autoLabel:
		v.askMerge("auto")
	case "Cancel":
		v.review = nil
	}
	return nil
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
	switch {
	case p.ReviewDecision == "CHANGES_REQUESTED":
		r.warn = "changes have been requested"
	case p.ReviewDecision == "REVIEW_REQUIRED":
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
	if r.confirm == "merge" || r.confirm == "auto" {
		heading = "Merge"
	}
	b.WriteString(ui.Bold.Render(fmt.Sprintf("%s %s#%d", heading, r.repo, r.num)))
	b.WriteString("\n\n")
	switch {
	case r.submitting:
		b.WriteString(ui.Faint.Render("submitting review…"))
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
			detail := "  " + v.cfg.ResolvedMergeMethod()
			if v.cfg.MergeDeleteBranch {
				detail += ", then delete the branch"
			}
			b.WriteString(ui.Faint.Render(detail) + "\n")
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
	r.submitting = true
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
			return mergeDoneMsg{err: ghErr(err, out), auto: auto}
		}
		return mergeDoneMsg{what: what, url: url, auto: auto}
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
	r.submitting = true
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
			return reviewDoneMsg{err: cmdErr(err)}
		}
		return reviewDoneMsg{what: what, url: url, state: state}
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

func (v *View) copySelected() tea.Cmd {
	p := v.list.Selected()
	if p.URL == "" {
		return nil
	}
	return func() tea.Msg {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(p.URL)
		_ = c.Run()
		return nil
	}
}

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
			set[i].Unread = v.unread[set[i].URL]
			set[i].UnreadGutter = v.unreadOn
			set[i].ShowLabels = labels
		}
	}

	if !v.showReview || (len(rev) == 0 && v.reviewErr == nil) {
		v.list.SetItems(v.groupSection(mine))
		return
	}
	if len(mine) == 0 && v.reviewErr == nil {
		v.list.SetItems(v.groupSection(rev))
		return
	}

	var items []pr
	if len(mine) > 0 {
		items = append(items, pr{Separator: sectionLabel("MY PULL REQUESTS", len(mine), 0, v.minePage.total)})
		items = append(items, v.groupSection(mine)...)
	}
	items = append(items, pr{Separator: v.reviewLabel(rev)})
	items = append(items, v.groupSection(rev)...)
	v.list.SetItems(items)
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
// or not notifications are on: the mark is how you catch up on what arrived
// while you were not looking, which is exactly when a notification is missed.
func (v *View) markUnread(prev, next []pr, seeded bool) {
	// The first load of a section is everything, not "new": marking it
	// would light up the whole list on startup.
	if !v.unreadOn || !seeded {
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

// markRead clears the selected row's mark when the preview is off and you
// asked for the detail explicitly. Hovering is enough only while the detail
// is on screen; with it hidden, a row you never opened is not read.
func (v *View) markRead() {
	v.clearUnread()
}

// saveCache rewrites the cache so a mark cleared (or earned) in this session
// survives a restart. Cheap: one small JSON file, written atomically.
func (v *View) saveCache() {
	_ = cache.Save(cacheName, cachedPRs{Mine: v.raw, Review: v.reviewRaw, Unread: v.unreadURLs()})
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
	v.list.SetSize(listW, max(1, h-v.headerRows())) // reserve the header rows
	v.bodyKey = ""                                  // width changed: invalidate the body cache
	// The label column depends on the list's width, so a resize across the
	// threshold has to re-decide it.
	if was != listW && v.seeded {
		v.applySort()
	}
}

func (v *View) ListView() string {
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
	// The effective query goes on its own line under the status, so what is
	// filtering the list is always visible rather than implied.
	if q := v.queryLine(); q != "" {
		header += "\n" + q
	}
	return header + "\n" + v.list.View()
}

// headerRows is how many rows ListView puts above the list, which SetSize
// has to reserve or the last row falls off the bottom.
func (v *View) headerRows() int {
	if q := v.queryLine(); q != "" {
		return 1 + lipgloss.Height(q) // status, then the boxed query
	}
	return 1
}

func (v *View) statusText() string {
	switch {
	case v.loading:
		return "Loading PRs…"
	case v.err != nil:
		return "Error (ctrl+r to retry)"
	case v.flash != "":
		return v.flash
	default:
		s := fmt.Sprintf("%d PRs", len(v.raw))
		if v.minePage.total > len(v.raw) {
			s = fmt.Sprintf("%d of %d PRs", len(v.raw), v.minePage.total)
		}
		if v.showReview && len(v.reviewRaw) > 0 {
			s += fmt.Sprintf(" +%d to review", len(v.reviewRaw))
			if v.reviewPage.total > len(v.reviewRaw) {
				s += fmt.Sprintf(" of %d", v.reviewPage.total)
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
	b.WriteString(ui.Bold.Width(v.prevW).Render(p.Title))
	b.WriteString("\n")
	b.WriteString(ui.Dim.Render(fmt.Sprintf("%s #%d  ·  @%s  ·  %s ago",
		p.repo(), p.Number, p.Author.Login, ui.Age(p.UpdatedAt))))
	b.WriteString("\n\n")

	// Status line: state · CI · review · diff · comments.
	fmt.Fprintf(&b, "%s %s   %s %s   %s %s\n",
		p.stateIcon(), stateWord(p), p.ciIcon(), ciWord(p), p.reviewIcon(), reviewWord(p))
	if d := p.diffCell(); d != "" {
		b.WriteString(d)
		b.WriteString("   ")
	}
	if c := p.commentsCell(); c != "" {
		b.WriteString(c)
	}
	if p.Mergeable == "CONFLICTING" {
		b.WriteString("   ")
		b.WriteString(ui.Red.Render("⚠ conflicts"))
	}
	b.WriteString("\n")

	if pills := labelPills(p.Labels.Nodes); pills != "" {
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
	default:
		// Description, then checks, then comments: the summary reads top to
		// bottom in the order you want it, with the detail panes (diff,
		// comments) staying bare because they are already the detail.
		b.WriteString(blockHeader("Description"))
		b.WriteString("\n")
		b.WriteString(v.renderedBody(p))
		if blk := v.checksBlock(p); blk != "" {
			b.WriteString("\n\n")
			b.WriteString(blockHeader("Checks"))
			b.WriteString("\n")
			b.WriteString(blk)
			if _, _, _, total := p.checkCounts(); total > 0 {
				b.WriteString("\n" + ui.Faint.Render(fmt.Sprintf("  %s or click %s",
					v.keyHint("jobs"), jobsMarker)))
			}
		}
		b.WriteString("\n\n")
		b.WriteString(v.commentsBlock(p))
	}
	return b.String()
}

// blockHeader labels a preview section. One style for all of them, so the
// pane reads as a list of sections rather than three unrelated widgets.
func blockHeader(name string) string {
	// Glyph gated like the other decorative icons, so a plain-font setup
	// gets the label without a tofu box.
	return ui.Dim.Render(ui.Glyph(ui.IconSection, "") + name)
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
		rows = append(rows, ui.Yellow.Render(ui.IconReviewReq+" Review required")+
			"\n"+ui.Dim.Render("  waiting on a reviewer"))
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
	head := blockHeader("Comments")
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
	return []key.Binding{v.keys.Open, v.keys.Diff, v.keys.Comments, v.keys.Jobs, v.keys.Start, v.keys.Copy, v.keys.Sort, v.keys.Rev, v.keys.Review}
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

func (v *View) InputActive() bool {
	return v.list.Filtering() || v.review != nil || v.rerun != nil || v.input != nil
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
