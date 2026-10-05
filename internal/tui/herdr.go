package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/herdr"
	"github.com/sanity-labs/agenda/internal/ui"
)

// Herdr mode (`agenda --herdr`, run from a Herdr popup): views hand their
// selection to the root model as a herdr.Request instead of opening it; the
// model has the Opener focus (or create) its workspace and tab, answering
// the opener's questions with a picker, then exits so the popup closes on
// the workspace.

// herdrSetter is implemented by views with a herdr mode.
type herdrSetter interface {
	SetHerdr(on bool)
}

// herdrDoneMsg reports a finished Opener.Open.
type herdrDoneMsg struct {
	req herdr.Request
	err error
}

// repoCandsMsg carries the clones to offer for a request that needs one.
type repoCandsMsg struct {
	req       herdr.Request
	workspace string // the workspace it belongs to, "" for a new one
	cands     []herdr.Candidate
}

// repoPickerRows is how many clones the repository picker shows at once.
const repoPickerRows = 12

// WithHerdr turns on herdr mode, handing requests to o.
func (m Model) WithHerdr(o *herdr.Opener) Model {
	m.herdr = o
	for _, v := range m.views {
		if h, ok := v.(herdrSetter); ok {
			h.SetHerdr(true)
		}
	}
	return m
}

// openHerdr starts opening req's workspace in the background. The footer
// names it meanwhile, and further requests wait for it.
func (m *Model) openHerdr(req herdr.Request) tea.Cmd {
	m.herdrBusy = req.Name()
	o := m.herdr
	return func() tea.Msg { return herdrDoneMsg{req: req, err: o.Open(req)} }
}

// herdrDone finishes a request: quit once the workspace is focused, ask
// where an unclaimed item goes or which clone a new branch goes in, and say
// what went wrong otherwise. Failures go to the message log, whose overlay
// shows the full error; git and herdr put the cause at the end of theirs.
func (m Model) herdrDone(msg herdrDoneMsg) (tea.Model, tea.Cmd) {
	m.herdrBusy = ""
	var choice *herdr.ChoiceError
	var repo *herdr.RepoError
	switch {
	case errors.As(msg.err, &choice):
		return m.showChooser(msg.req, choice.Choices)
	case errors.As(msg.err, &repo):
		return m, m.listRepos(msg.req, repo.Workspace)
	case msg.err != nil:
		return m, herdrError(msg.req, msg.err.Error())
	}
	return m, tea.Quit
}

// herdrError reports a request that could not be opened.
func herdrError(req herdr.Request, detail string) tea.Cmd {
	s := ui.Status(ui.SeverityError, "Herdr", "Couldn't open "+req.Name(), detail)
	return func() tea.Msg { return s }
}

// showChooser asks where a request goes that no workspace claims yet.
func (m Model) showChooser(req herdr.Request, choices []herdr.Choice) (tea.Model, tea.Cmd) {
	items := make([]ui.PickerItem, len(choices))
	for i, c := range choices {
		items[i] = ui.PickerItem{Label: c.Label}
	}
	p := ui.NewPicker(req.Name()+" has no workspace yet", items)
	m.chooser, m.herdrReq, m.choices = &p, req, choices
	return m, nil
}

// updateChooser routes a key to the open chooser; a choice retries the
// request with it.
func (m Model) updateChooser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.chooser.Update(msg) {
	case ui.PickerCancel:
		m.chooser = nil
	case ui.PickerConfirm:
		req := m.herdrReq
		c := m.choices[m.chooser.Index()]
		req.Claim = &c
		m.chooser = nil
		return m, m.openHerdr(req)
	}
	return m, nil
}

// listRepos gathers the clones to pick from: those the workspace already
// works in first, then the one agenda's popup was opened over, then the rest,
// most recently used first.
func (m Model) listRepos(req herdr.Request, workspace string) tea.Cmd {
	o := m.herdr
	return func() tea.Msg {
		cands := o.Repos().List()
		var dirs []string
		if workspace != "" {
			dirs = o.Dirs(workspace)
		}
		dirs = append(dirs, herdr.SourceCwd())
		cands = frontload(cands, dirs)
		return repoCandsMsg{req: req, workspace: workspace, cands: cands}
	}
}

// frontload moves the clones containing dirs to the front, in dirs' order.
func frontload(cands []herdr.Candidate, dirs []string) []herdr.Candidate {
	var front, rest []herdr.Candidate
	picked := map[int]bool{}
	for _, d := range dirs {
		if i := herdr.Containing(cands, d); i >= 0 && !picked[i] {
			picked[i] = true
			front = append(front, cands[i])
		}
	}
	for i, c := range cands {
		if !picked[i] {
			rest = append(rest, c)
		}
	}
	return append(front, rest...)
}

// showRepoPicker opens the repository picker for a request. For an issue
// that already has a workspace, the first row opens it without a branch.
func (m Model) showRepoPicker(msg repoCandsMsg) (tea.Model, tea.Cmd) {
	if len(msg.cands) == 0 && msg.workspace == "" {
		return m, herdrError(msg.req, "no local clones found under "+m.herdr.Repos().Root)
	}
	var items []ui.PickerItem
	m.repoFocusRow = msg.workspace != ""
	if m.repoFocusRow {
		items = append(items, ui.PickerItem{Label: "Just open the workspace, no branch yet"})
	}
	for _, c := range msg.cands {
		items = append(items, ui.PickerItem{Label: c.Slug})
	}
	p := ui.NewFilterPicker("Start "+msg.req.Name()+" in…", items, repoPickerRows)
	m.repoPicker, m.herdrReq, m.repoCands = &p, msg.req, msg.cands
	return m, nil
}

// updateRepoPicker routes a key to the open repository picker; choosing a
// clone (or to open the workspace as it is) retries the request.
func (m Model) updateRepoPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.repoPicker.Update(msg) {
	case ui.PickerCancel:
		m.repoPicker = nil
	case ui.PickerConfirm:
		req := m.herdrReq
		i := m.repoPicker.Index()
		if m.repoFocusRow {
			i--
		}
		if i < 0 {
			req.FocusOnly = true
		} else {
			req.Clone = m.repoCands[i].Path
		}
		m.repoPicker = nil
		return m, m.openHerdr(req)
	}
	return m, nil
}
