package herdr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The model: a workspace claims nodes of Linear's tree (an issue, a parent
// issue with its sub-issues, or a whole project) or a PR with no issue. An
// item opens in the workspace claiming the nearest of its nodes, in a tab per
// checkout: each branch the item is worked on gets a git worktree, and a tab
// whose panes start there. When nothing claims an item, the caller is asked
// once where it goes.

// Request asks for the workspace of a selected item. Views send it as a
// tea.Msg; the root model hands it to an Opener, answering its questions
// (ChoiceError, RepoError) by filling in the request and opening again.
type Request struct {
	// Issues are the Linear issues the item belongs to, most relevant first:
	// the issue itself, or a PR's linked issues.
	Issues []Issue
	// PR is the pull request itself, or an issue's open PR. It supplies the
	// repository and the branch to check out.
	PR *PR
	// Session, when set, asks for an agent session instead: focus the pane
	// running it, or resume it in a workspace for its directory.
	Session *Session

	// Claim answers a ChoiceError: where a request nothing claims goes.
	Claim *Choice
	// Clone answers a RepoError: the clone to start the issue's branch in.
	Clone string
	// FocusOnly answers a RepoError by opening the workspace without
	// starting a branch.
	FocusOnly bool
}

// Issue is a Linear issue and where it sits in Linear's tree.
type Issue struct {
	ID          string // e.g. SRE-5287
	Title       string
	Branch      string // Linear's suggested branch name
	Parent      string // parent issue's identifier, for a sub-issue
	ParentTitle string
	ProjectID   string
	Project     string // the project's name
}

type PR struct {
	Repo   string // owner/name
	Number int
	Title  string
	Branch string // head branch; "" looks it up with gh
}

type Session struct {
	ID     string // the agent's session id
	Cwd    string
	Title  string
	Resume []string // command that resumes the session in Cwd
}

// Choice is one place a request nothing claims can go: a new workspace for
// one of its nodes, or one of them added to an existing workspace.
type Choice struct {
	Label     string // how the chooser shows it
	Node      Node   // what the workspace will claim
	Workspace string // workspace to add the node to; "" creates one
	NewLabel  string // label for a new workspace
}

// ChoiceError asks where a request goes that no workspace claims yet. The
// caller picks one of Choices, sets Request.Claim and opens again.
type ChoiceError struct{ Choices []Choice }

func (e *ChoiceError) Error() string { return "choose where this goes" }

// RepoError asks which clone to start an issue's branch in: the issue has no
// PR to name one. Workspace, when set, is the existing workspace it belongs
// to, which can be opened without starting a branch (Request.FocusOnly).
type RepoError struct{ Workspace string }

func (e *RepoError) Error() string { return "pick a repository for the issue's branch" }

// Name is how the request is described to the user ("SRE-5287", "agenda#28").
func (r Request) Name() string {
	switch {
	case r.Session != nil:
		return "session " + firstNonEmpty(r.Session.Title, r.Session.ID)
	case len(r.Issues) > 0:
		return r.Issues[0].ID
	case r.PR != nil:
		return prKey(*r.PR)
	}
	return "workspace"
}

// Opener finds or creates workspaces and focuses them.
type Opener struct {
	herdr        *Client
	run          Runner // git and gh
	repos        Repos
	worktreesDir string // where new worktrees go, as <repo>/<branch>
	claims       *Claims
}

func NewOpener(run Runner, repos Repos, worktreesDir string, claims *Claims) *Opener {
	return &Opener{herdr: NewClient(run), run: run, repos: repos, worktreesDir: worktreesDir, claims: claims}
}

// Repos is the clone lookup the opener uses, for listing picker candidates.
func (o *Opener) Repos() Repos { return o.repos }

// Dirs are the working directories of workspace's panes, for ranking the
// clones it already works in first.
func (o *Opener) Dirs(workspace string) []string {
	panes, _ := o.herdr.Panes()
	var out []string
	for _, p := range panes {
		if p.WorkspaceID == workspace {
			out = append(out, p.Cwd)
		}
	}
	return out
}

// Open focuses the request's checkout, creating its worktree, tab or
// workspace as needed. It returns a ChoiceError or RepoError when it needs
// an answer first.
func (o *Opener) Open(req Request) error {
	if req.Session != nil {
		return o.openSession(*req.Session)
	}
	if len(req.Issues) == 0 && req.PR == nil {
		return errors.New("nothing to open")
	}
	live, err := o.herdr.Workspaces()
	if err != nil {
		return err
	}
	ws, node := o.owner(req, live)
	newLabel := ""
	if ws == "" {
		if req.Claim == nil {
			return &ChoiceError{Choices: o.choices(req, live)}
		}
		ws, node, newLabel = req.Claim.Workspace, req.Claim.Node, req.Claim.NewLabel
	}
	return o.place(req, ws, node, newLabel, live)
}

// owner finds the workspace for req: the one claiming the nearest of its
// nodes (issue, then parent, then project, for each linked issue in turn),
// else one whose label names an issue or the PR.
func (o *Opener) owner(req Request, live []Workspace) (string, Node) {
	for _, iss := range req.Issues {
		for _, n := range iss.nodes() {
			if ws := o.claims.Owner(n, live); ws != "" {
				return ws, n
			}
		}
	}
	for _, iss := range req.Issues {
		if ws := findWorkspace(live, iss.ID); ws != "" {
			return ws, Node{"issue", iss.ID}
		}
	}
	if len(req.Issues) == 0 {
		n := Node{"pr", prKey(*req.PR)}
		if ws := o.claims.Owner(n, live); ws != "" {
			return ws, n
		}
		if ws := findWorkspace(live, n.Key); ws != "" {
			return ws, n
		}
	}
	return "", Node{}
}

// nodes are the claimable nodes an issue sits under, nearest first.
func (i Issue) nodes() []Node {
	out := []Node{{"issue", i.ID}}
	if i.Parent != "" {
		out = append(out, Node{"issue", i.Parent})
	}
	if i.ProjectID != "" {
		out = append(out, Node{"project", i.ProjectID})
	}
	return out
}

// choices are the places an unclaimed request can go: a new workspace for
// the issue, its parent or its project (or for the PR), or one of those added
// to the workspace the popup was opened over.
func (o *Opener) choices(req Request, live []Workspace) []Choice {
	type option struct {
		node     Node
		what     string // as in "New workspace for <what>"
		addWhat  string // as in "Add <addWhat> to <workspace>"
		newLabel string
	}
	var opts []option
	if len(req.Issues) > 0 {
		iss := req.Issues[0]
		title := iss.Title
		if title == "" && req.PR != nil {
			title = req.PR.Title
		}
		l := joinLabel(iss.ID, title)
		opts = append(opts, option{Node{"issue", iss.ID}, l, iss.ID, l})
		if iss.Parent != "" {
			pl := joinLabel(iss.Parent, iss.ParentTitle)
			opts = append(opts, option{Node{"issue", iss.Parent}, "parent " + pl, iss.Parent + " and its sub-issues", pl})
		}
		if iss.ProjectID != "" {
			opts = append(opts, option{Node{"project", iss.ProjectID}, "project " + iss.Project, "project " + iss.Project, iss.Project})
		}
	} else {
		k := prKey(*req.PR)
		l := joinLabel(k, req.PR.Title)
		opts = append(opts, option{Node{"pr", k}, l, k, l})
	}

	var out []Choice
	for _, op := range opts {
		out = append(out, Choice{Label: "New workspace for " + op.what, Node: op.node, NewLabel: op.newLabel})
	}
	if src := sourceWorkspace(live); src != nil {
		for _, op := range opts {
			out = append(out, Choice{Label: "Add " + op.addWhat + " to " + baseLabel(src.Label), Node: op.node, Workspace: src.ID})
		}
	}
	return out
}

// place puts req's checkout in workspace ws (a new one labelled newLabel when
// ws is ""): it focuses the tab already on the checkout, or creates the
// worktree and a tab for it. Then ws claims node.
func (o *Opener) place(req Request, ws string, node Node, newLabel string, live []Workspace) error {
	var path, tab string
	if req.PR != nil {
		pr := *req.PR
		if pr.Branch == "" {
			b, err := o.headBranch(pr)
			if err != nil {
				return err
			}
			pr.Branch = b
		}
		clone, ok := o.repos.Clone(pr.Repo)
		if !ok {
			return fmt.Errorf("no local clone of %s under %s (set herdr.repos in the config)", pr.Repo, o.repos.Root)
		}
		p, err := o.checkout(clone, pr.Branch, &pr)
		if err != nil {
			return err
		}
		path, tab = p, prKey(pr)
	} else {
		iss := req.Issues[0]
		if ws != "" {
			if t := o.issueTab(ws, iss.ID); t != "" {
				return o.focus(ws, t, node, live)
			}
		}
		existing := o.findWorktreeDir(iss.ID)
		switch {
		case existing != "":
			path, tab = existing, filepath.Base(filepath.Dir(existing))
		case req.FocusOnly && ws != "":
			return o.focus(ws, "", node, live)
		case req.Clone == "":
			return &RepoError{Workspace: ws}
		default:
			p, err := o.checkout(req.Clone, issueBranch(iss), nil)
			if err != nil {
				return err
			}
			path, tab = p, filepath.Base(req.Clone)
		}
	}
	if len(req.Issues) > 0 {
		tab = req.Issues[0].ID + " · " + tab
	}

	if ws == "" {
		r, err := o.herdr.CreateWorkspace(path, newLabel)
		if err != nil {
			return err
		}
		if r.Tab.TabID != "" {
			_ = o.herdr.RenameTab(r.Tab.TabID, tab)
		}
		return o.claims.Add(r.Workspace.ID, r.Workspace.Label, node)
	}
	if t := o.tabAt(ws, path); t != "" {
		return o.focus(ws, t, node, live)
	}
	if err := o.herdr.FocusWorkspace(ws); err != nil {
		return err
	}
	if _, err := o.herdr.CreateTab(ws, path, tab); err != nil {
		return err
	}
	return o.claims.Add(ws, labelOf(live, ws), node)
}

// focus brings up workspace ws and its tab t ("" leaves the tab as it was),
// and records that ws claims node.
func (o *Opener) focus(ws, t string, node Node, live []Workspace) error {
	if err := o.herdr.FocusWorkspace(ws); err != nil {
		return err
	}
	if t != "" {
		if err := o.herdr.FocusTab(t); err != nil {
			return err
		}
	}
	return o.claims.Add(ws, labelOf(live, ws), node)
}

// issueTab is the tab of ws labelled for the issue, or "".
func (o *Opener) issueTab(ws, id string) string {
	tabs, _ := o.herdr.Tabs(ws)
	for _, t := range tabs {
		if mentions(t.Label, id) {
			return t.TabID
		}
	}
	return ""
}

// tabAt is the tab of ws with a pane inside path, or "".
func (o *Opener) tabAt(ws, path string) string {
	panes, _ := o.herdr.Panes()
	path = realPath(path)
	for _, p := range panes {
		if p.WorkspaceID == ws && within(realPath(p.Cwd), path) {
			return p.TabID
		}
	}
	return ""
}

// openSession focuses the pane already running the session, else resumes it
// in a new tab of a workspace with a pane in its directory, or in a new
// workspace there.
func (o *Opener) openSession(s Session) error {
	agents, err := o.herdr.Agents()
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.Session != nil && a.Session.Value == s.ID {
			return o.herdr.FocusAgent(a.PaneID)
		}
	}
	if fi, err := os.Stat(s.Cwd); err != nil || !fi.IsDir() {
		return fmt.Errorf("the session's directory %s no longer exists", s.Cwd)
	}

	panes, err := o.herdr.Panes()
	if err != nil {
		return err
	}
	ws := ""
	for _, p := range panes {
		if p.Cwd == s.Cwd {
			ws = p.WorkspaceID
			break
		}
	}
	title := truncate(firstNonEmpty(s.Title, filepath.Base(s.Cwd)), 30)
	if ws == "" {
		r, err := o.herdr.CreateWorkspace(s.Cwd, title)
		if err != nil {
			return err
		}
		return o.herdr.Run(r.RootPane.PaneID, shellJoin(s.Resume))
	}
	if err := o.herdr.FocusWorkspace(ws); err != nil {
		return err
	}
	pane, err := o.herdr.CreateTab(ws, s.Cwd, title)
	if err != nil {
		return err
	}
	return o.herdr.Run(pane, shellJoin(s.Resume))
}

// sourceWorkspace is the workspace agenda's popup was opened over (Herdr
// passes it to popups as HERDR_ACTIVE_WORKSPACE_ID), or the one agenda runs
// in, or nil.
func sourceWorkspace(live []Workspace) *Workspace {
	id := firstNonEmpty(os.Getenv("HERDR_ACTIVE_WORKSPACE_ID"), os.Getenv("HERDR_WORKSPACE_ID"))
	for i := range live {
		if live[i].ID == id {
			return &live[i]
		}
	}
	return nil
}

// SourceCwd is the directory of the pane agenda's popup was opened over, or
// agenda's own.
func SourceCwd() string {
	if d := os.Getenv("HERDR_ACTIVE_PANE_CWD"); d != "" {
		return d
	}
	d, _ := os.Getwd()
	return d
}

// findWorkspace returns the workspace whose label or worktree path mentions
// key, or "".
func findWorkspace(workspaces []Workspace, key string) string {
	for _, w := range workspaces {
		if mentions(w.Label, key) {
			return w.ID
		}
		if w.Worktree != nil && mentions(w.Worktree.CheckoutPath, key) {
			return w.ID
		}
	}
	return ""
}

func labelOf(live []Workspace, id string) string {
	for _, w := range live {
		if w.ID == id {
			return w.Label
		}
	}
	return ""
}

// mentions reports whether s contains key as a whole token, ignoring case:
// SRE-52 is not in SRE-5287, but it is in "obliadp/sre-52-fix".
func mentions(s, key string) bool {
	if key == "" {
		return false
	}
	re := regexp.MustCompile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(key) + `($|[^a-z0-9])`)
	return re.MatchString(s)
}

// joinLabel is "<key> <title>", the title shortened for Herdr's sidebar.
func joinLabel(key, title string) string {
	return strings.TrimSpace(key + " " + truncate(title, 40))
}

// issueBranch is the branch for an issue with no PR: Linear's suggested name,
// else one derived from the identifier and title.
func issueBranch(iss Issue) string {
	if iss.Branch != "" {
		return iss.Branch
	}
	return strings.ToLower(iss.ID) + slugSuffix(iss.Title)
}

// slugSuffix is "-" plus the title's first few words in branch-safe form.
func slugSuffix(title string) string {
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if len(words) == 5 {
			break
		}
		words = append(words, w)
	}
	if len(words) == 0 {
		return ""
	}
	return "-" + strings.Join(words, "-")
}

// prKey names a PR: repo name and number, e.g. "agenda#28".
func prKey(pr PR) string {
	name := pr.Repo
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return name + "#" + strconv.Itoa(pr.Number)
}

// shellJoin quotes args for a POSIX shell command line.
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@") == "" {
			q[i] = a
			continue
		}
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
