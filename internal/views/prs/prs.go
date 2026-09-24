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
	// Reviewed marks a review-requested row the viewer has already reviewed
	// (set at assembly when github.mark_reviewed is on): rendered dim with a
	// "reviewed" tag so the eye can skip it.
	Reviewed   bool `json:"-"`
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
		Nodes []struct {
			Commit struct {
				StatusCheckRollup struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (p pr) repo() string { return p.Repository.NameWithOwner }

// reviewedByMe reports whether the viewer's latest review still counts as
// "handled": approved, changes requested, or commented. DISMISSED and PENDING
// mean the ball is back with the viewer.
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
	}
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
	return ui.Green.Render("+"+strconv.Itoa(p.Additions)) + " " +
		ui.Red.Render("-"+strconv.Itoa(p.Deletions))
}

// diffPlain / commentsPlain are the uncolored cell texts, for dim rows.
func (p pr) diffPlain() string {
	if p.Additions == 0 && p.Deletions == 0 {
		return ""
	}
	return fmt.Sprintf("+%d -%d", p.Additions, p.Deletions)
}

func (p pr) commentsPlain() string {
	if p.Comments.TotalCount == 0 {
		return ""
	}
	return fmt.Sprintf("%s%d", ui.IconComment, p.Comments.TotalCount)
}

func (p pr) commentsCell() string {
	if p.Comments.TotalCount == 0 {
		return ""
	}
	return ui.Dim.Render(fmt.Sprintf("%s%d", ui.IconComment, p.Comments.TotalCount))
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

	// Right cluster: diff · comments · age.
	right := strings.TrimSpace(p.diffCell() + "  " + p.commentsCell() + "  " + ui.Dim.Render(ui.Age(p.UpdatedAt)))

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
		right = ui.Dim.Render(strings.TrimSpace(p.diffPlain() + "  " + p.commentsPlain() + "  " + ui.Age(p.UpdatedAt)))
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
	prs []pr
	err error
}

type reviewListMsg struct {
	prs []pr
	err error
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
	body    string

	// pane picks what the right pane shows for the selection: description,
	// diff ('d'), or comments ('c'). diffs and comments cache fetched data
	// by PR URL for the session.
	pane     paneMode
	diffs    map[string]diffState
	comments map[string]*commentsState

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
)

// reviewFlow drives the review popup: pick an option, then (for comment /
// request-changes) type a body, then submit via gh.
type reviewFlow struct {
	url, repo  string
	num        int
	sel        int    // cursor over reviewOptions while picking
	verdict    string // "", then "approve" | "comment" | "request-changes"
	body       string
	submitting bool
}

// reviewOptions are the popup's entries: a hotkey, a label, and the gh
// verdict ("" for the non-submit entries).
var reviewOptions = []struct {
	key, label, verdict string
}{
	{"a", "Approve", "approve"},
	{"c", "Comment", "comment"},
	{"x", "Request changes", "request-changes"},
	{"d", "View diff", ""},
	{"", "Cancel", ""},
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
		},
	}
	v.list.SetRowHeight(2) // two-line rows: metadata + title
	v.list.Rebind(func(a string, d ...string) []string { return km.Of("list", a, d...) })

	// Paint last run's PRs immediately; the live fetch refreshes them.
	if cached, ok := cache.Load[cachedPRs](cacheName); ok && len(cached.Mine)+len(cached.Review) > 0 {
		v.raw, v.reviewRaw = cached.Mine, cached.Review
		v.seeded = true
		v.applySort()
		v.publish(append(cached.Mine, cached.Review...))
		v.loading = false
	}
	return v
}

// cachedPRs is the on-disk shape of the last fetch.
type cachedPRs struct {
	Mine   []pr `json:"mine"`
	Review []pr `json:"review"`
}

const cacheName = "prs"

func (v *View) Title() string { return "PRs" }

func (v *View) Init() tea.Cmd {
	v.loading = true
	return v.fetch()
}

func (v *View) Loading() bool { return v.loading || v.reviewLoading }

// graphqlQuery is one PR search. The own-PRs and review-requested searches
// run as two separate requests; combining them into one aliased query makes
// GitHub's gateway time out (502) on real accounts.
const graphqlQuery = `query($q: String!) {
  search(query: $q, type: ISSUE, first: 100) { nodes { ...prFields } }
}
fragment prFields on PullRequest {
  number title url state isDraft updatedAt headRefName
  additions deletions mergeable reviewDecision body
  viewerLatestReview { state }
  author { login }
  repository { nameWithOwner }
  comments { totalCount }
  labels(first: 6) { nodes { name color } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

// ensurePR appends "is:pr" to a search query when absent (the search API
// returns issues too under type:ISSUE).
func ensurePR(q string) string {
	if strings.Contains(q, "is:pr") {
		return q
	}
	return strings.TrimSpace(q + " is:pr")
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
func searchPRs(q string) ([]pr, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		// GitHub's gateway gives up at ~10s; anything past 30s is a hung gh.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, "gh", "api", "graphql",
			"-f", "query="+graphqlQuery,
			"-f", "q="+q,
			"--jq", ".data.search.nodes",
		).Output()
		cancel()
		if err != nil {
			lastErr = cmdErr(err)
			continue
		}
		var prs []pr
		if err := json.Unmarshal(out, &prs); err != nil {
			lastErr = fmt.Errorf("parsing gh output: %w", err)
			continue
		}
		return dropEmpty(prs), nil
	}
	if lastErr != nil && strings.Contains(lastErr.Error(), "invalid character '<'") {
		return nil, fmt.Errorf("GitHub returned an error page (transient 5xx), retry with ctrl+r")
	}
	return nil, fmt.Errorf("gh api graphql: %w", lastErr)
}

func (v *View) fetch() tea.Cmd {
	q := ensurePR(v.cfg.Filter)
	cmds := []tea.Cmd{func() tea.Msg {
		prs, err := searchPRs(q)
		return mineMsg{prs: prs, err: err}
	}}
	// The review search only runs when something consumes it: the visible
	// section, or review-request notifications. Otherwise the view does
	// exactly one search, as it originally did.
	if v.showReview || v.notifier != nil {
		cmds = append(cmds, v.fetchReview())
	}
	return tea.Batch(cmds...)
}

// fetchReview runs just the review-requested search.
func (v *View) fetchReview() tea.Cmd {
	rq := ensurePR(v.cfg.ReviewFilter)
	v.reviewLoading = true
	return func() tea.Msg {
		prs, err := searchPRs(rq)
		return reviewListMsg{prs: prs, err: err}
	}
}

func (v *View) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case mineMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return nil
		}
		v.err = nil
		v.flash = ""
		v.raw = msg.prs
		v.applySort()
		v.publish(append(msg.prs, v.reviewRaw...))
		_ = cache.Save(cacheName, cachedPRs{Mine: msg.prs, Review: v.reviewRaw})
		return nil
	case reviewListMsg:
		v.reviewLoading = false
		if msg.err != nil {
			v.reviewErr = msg.err
			v.applySort() // the section separator shows the failure
			return nil
		}
		v.reviewErr = nil
		cmd := v.notifyNewReviews(v.reviewRaw, msg.prs)
		v.reviewRaw = msg.prs
		v.seeded = true
		v.applySort()
		v.publish(append(v.raw, msg.prs...))
		_ = cache.Save(cacheName, cachedPRs{Mine: v.raw, Review: msg.prs})
		return cmd
	case diffMsg:
		v.diffs[msg.url] = diffState{text: msg.text, err: msg.err, done: true}
		return nil
	case settleMsg:
		if msg.gen != v.settleGen {
			return nil // superseded by further navigation
		}
		return tea.Batch(v.maybeFetchDiff(), v.maybeFetchComments())
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
		// Review submitted: done looking, so a transient reveal folds away.
		return tea.Batch(ui.ConcealPreview, v.fetch())
	case commentsMsg:
		if st, ok := v.comments[msg.url]; ok {
			st.data, st.err, st.done = msg.data, msg.err, true
			v.commentsRev++
		}
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
		if v.input != nil {
			return v.updateThreadInput(msg)
		}
		before := v.list.Selected().URL
		if consumed, cmd := v.list.Update(msg); consumed {
			// Selection may have moved while a data pane is showing; fetch
			// for the new selection only once it settles, so holding j/k
			// doesn't spawn a gh call per row scrolled past.
			v.annIdx = 0
			if v.list.Selected().URL != before {
				// Moving on ends a transient preview reveal.
				return tea.Batch(cmd, v.scheduleSettle(), ui.ConcealPreview)
			}
			return tea.Batch(cmd, v.scheduleSettle())
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
		case key.Matches(msg, v.keys.Comments):
			return v.setPane(paneComments)
		case key.Matches(msg, v.keys.NextThread):
			return v.jumpThread(1)
		case key.Matches(msg, v.keys.PrevThread):
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

// setPane toggles the right pane between the description and the given mode,
// kicking off whatever fetch that pane needs.
func (v *View) setPane(mode paneMode) tea.Cmd {
	if v.pane == mode {
		v.pane = paneBody
		return ui.ConcealPreview
	}
	v.pane = mode
	v.annIdx = 0
	// The pane is about to show a diff or comments; a hidden preview would
	// swallow it silently.
	return tea.Batch(ui.RevealPreview, v.maybeFetchDiff(), v.maybeFetchComments())
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
	if r.verdict == "" {
		switch msg.String() {
		case "up", "k":
			r.sel = (r.sel - 1 + len(reviewOptions)) % len(reviewOptions)
			return nil
		case "down", "j":
			r.sel = (r.sel + 1) % len(reviewOptions)
			return nil
		case "enter":
			return v.activateReviewOption(reviewOptions[r.sel].label)
		case "esc", "q", "ctrl+c":
			v.review = nil
			return nil
		default:
			for _, opt := range reviewOptions {
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
	case "Cancel":
		v.review = nil
	}
	return nil
}

// Overlay implements the root model's view-modal hook: the review popup.
func (v *View) Overlay() string {
	r := v.review
	if r == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(ui.Bold.Render(fmt.Sprintf("Review %s#%d", r.repo, r.num)))
	b.WriteString("\n\n")
	switch {
	case r.submitting:
		b.WriteString(ui.Faint.Render("submitting review…"))
	case r.verdict == "":
		for i, opt := range reviewOptions {
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
// and there are any, a separator and the review-requested PRs, sorted the
// same way.
func (v *View) applySort() {
	items := v.groupSection(sortPRs(v.raw, v.sort, v.rev))
	if v.showReview && (len(v.reviewRaw) > 0 || v.reviewErr != nil) {
		label := "Review Requested"
		if v.reviewErr != nil {
			label += "  ·  fetch failed (ctrl+r)"
		}
		items = append(items, pr{Separator: label})
		rev := sortPRs(v.reviewRaw, v.sort, v.rev)
		if v.cfg.MarkReviewed {
			for i := range rev {
				rev[i].Reviewed = rev[i].reviewedByMe()
			}
		}
		items = append(items, v.groupSection(rev)...)
	}
	v.list.SetItems(items)
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
	n := v.notifier
	return func() tea.Msg { return n.Notify(title, body) }
}

// ScrollList moves the list selection by n rows (mouse wheel).
func (v *View) ScrollList(n int) { v.list.ScrollBy(n) }

func (v *View) SetSize(listW, prevW, h int) {
	v.listW, v.prevW, v.height = listW, prevW, h
	v.list.SetSize(listW, max(1, h-1)) // reserve a row for the header line
	v.bodyKey = ""                     // width changed: invalidate the body cache
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
	default:
		s := fmt.Sprintf("%d PRs", len(v.raw))
		if v.showReview && len(v.reviewRaw) > 0 {
			s += fmt.Sprintf(" +%d to review", len(v.reviewRaw))
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
	default:
		b.WriteString(v.renderedBody(p))
	}
	return b.String()
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
	key := fmt.Sprintf("%d:%d:%d", p.Number, v.prevW, ui.PaletteGen())
	if v.bodyKey == key {
		return v.body
	}
	out := ui.Markdown(body, v.prevW)
	v.bodyKey, v.body = key, out
	return out
}

func (v *View) Bindings() []key.Binding {
	return []key.Binding{v.keys.Open, v.keys.Diff, v.keys.Comments, v.keys.Start, v.keys.Copy, v.keys.Sort, v.keys.Rev, v.keys.Review}
}

func (v *View) Status() string {
	return ui.Dim.Render(v.statusText())
}

func (v *View) InputActive() bool {
	return v.list.Filtering() || v.review != nil || v.input != nil
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
func cmdErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}
