package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/herdr"
	"github.com/sanity-labs/agenda/internal/ui"
)

// herdrFake answers herdr and git commands from canned output; see
// herdr's own tests for the fuller version.
type herdrFake struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
}

func (f *herdrFake) run(_, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, cmd)
	if f.fail[cmd] {
		return nil, errors.New("exit status 1")
	}
	if out, ok := f.out[cmd]; ok {
		return []byte(out), nil
	}
	return []byte(`{"result":{}}`), nil
}

func newHerdrModel(t *testing.T, f *herdrFake, root string) Model {
	t.Helper()
	t.Setenv("HERDR_BIN_PATH", "herdr")
	t.Setenv("HERDR_ACTIVE_WORKSPACE_ID", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_ACTIVE_PANE_CWD", "")
	claims := herdr.LoadClaims(filepath.Join(t.TempDir(), "claims.json"))
	o := herdr.NewOpener(f.run, herdr.Repos{Root: root}, filepath.Join(root, ".worktrees"), claims)
	return newClickModel(&stubView{"Linear"}).WithHerdr(o)
}

// drain runs cmd and feeds its message back into the model, the way the
// event loop would, returning the model and the next command.
func drain(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	got, next := m.Update(cmd())
	return got.(Model), next
}

func enter(m Model) (Model, tea.Cmd) {
	got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return got.(Model), cmd
}

func TestHerdrRequestQuitsOnceFocused(t *testing.T) {
	f := &herdrFake{out: map[string]string{
		"herdr workspace list": `{"result":{"workspaces":[{"workspace_id":"w7","label":"SRE-1 Fix"}]}}`,
	}}
	m := newHerdrModel(t, f, t.TempDir())

	got, cmd := m.Update(herdr.Request{Issues: []herdr.Issue{{ID: "SRE-1"}}, FocusOnly: true})
	m = got.(Model)
	if m.herdrBusy != "SRE-1" {
		t.Errorf("herdrBusy = %q while opening, want SRE-1", m.herdrBusy)
	}
	if again, _ := m.Update(herdr.Request{Issues: []herdr.Issue{{ID: "SRE-2"}}}); again.(Model).herdrBusy != "SRE-1" {
		t.Error("a second request started while the first was running")
	}

	m, cmd = drain(t, m, cmd)
	if _, quit := cmd().(tea.QuitMsg); !quit || m.herdrBusy != "" {
		t.Errorf("after focusing: quit=%v busy=%q, want agenda to exit", quit, m.herdrBusy)
	}
	if !slices.Contains(f.calls, "herdr workspace focus w7") {
		t.Errorf("calls = %q, want w7 focused", f.calls)
	}
}

func TestHerdrRequestFailureIsLogged(t *testing.T) {
	f := &herdrFake{fail: map[string]bool{"herdr workspace list": true}}
	m := newHerdrModel(t, f, t.TempDir())

	_, cmd := m.Update(herdr.Request{Issues: []herdr.Issue{{ID: "SRE-1"}}})
	m, cmd = drain(t, m, cmd)
	st, ok := cmd().(ui.StatusMsg)
	if !ok || st.Severity != ui.SeverityError || !strings.Contains(st.Summary, "SRE-1") || !strings.Contains(st.Detail, "herdr workspace list") {
		t.Errorf("got %#v, want an error naming the issue, with the failed command in its detail", st)
	}
	// An error with detail opens the message overlay, so the cause at the
	// end of a long git or herdr message is not truncated away.
	if got, _ := m.Update(st); !got.(Model).statusOpen {
		t.Error("the error's detail overlay did not open")
	}
}

// An issue nobody claims: choose where it goes, then which clone its branch
// starts in, then the workspace is created.
func TestHerdrUnclaimedIssueAsksWhereThenWhichRepo(t *testing.T) {
	root := t.TempDir()
	for _, r := range []string{"sanity-io/ops", "sanity-labs/agenda"} {
		if err := os.MkdirAll(filepath.Join(root, r, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	agenda := filepath.Join(root, "sanity-labs", "agenda")
	f := &herdrFake{out: map[string]string{
		"herdr workspace list": `{"result":{"workspaces":[]}}`,
	}}
	m := newHerdrModel(t, f, root)
	// The popup was opened over a pane in the agenda clone: it comes first.
	t.Setenv("HERDR_ACTIVE_PANE_CWD", agenda+"/internal")

	_, cmd := m.Update(herdr.Request{Issues: []herdr.Issue{{ID: "SRE-3", Title: "Thing", Branch: "sre-3-x"}}})
	m, _ = drain(t, m, cmd)
	if m.chooser == nil || m.choices[0].Label != "New workspace for SRE-3 Thing" {
		t.Fatalf("no chooser, or wrong first choice: %+v", m.choices)
	}

	m, cmd = enter(m)         // new workspace for the issue
	m, cmd = drain(t, m, cmd) // → which repo?
	m, _ = drain(t, m, cmd)   // clones → picker
	if m.chooser != nil || m.repoPicker == nil {
		t.Fatal("no repository picker after choosing a new workspace")
	}
	if m.repoFocusRow || m.repoCands[0].Slug != "sanity-labs/agenda" {
		t.Errorf("focus row=%v first=%q; want no 'just open' row for a new workspace, the source repo first", m.repoFocusRow, m.repoCands[0].Slug)
	}

	m, cmd = enter(m)
	if m.repoPicker != nil || m.herdrBusy != "SRE-3" {
		t.Fatalf("after choosing: picker open=%v busy=%q", m.repoPicker != nil, m.herdrBusy)
	}
	drain(t, m, cmd)
	if !slices.ContainsFunc(f.calls, func(c string) bool {
		return strings.HasPrefix(c, "herdr workspace create --cwd "+filepath.Join(root, ".worktrees", "agenda", "sre-3-x"))
	}) {
		t.Errorf("calls = %q, want a workspace on the new worktree in the chosen clone", f.calls)
	}
}

func TestHerdrRepoPickerCanJustOpenTheWorkspace(t *testing.T) {
	f := &herdrFake{out: map[string]string{
		"herdr workspace list": `{"result":{"workspaces":[{"workspace_id":"wB","label":"SRE-2999 CloudSQL"}]}}`,
	}}
	m := newHerdrModel(t, f, t.TempDir())

	_, cmd := m.Update(herdr.Request{Issues: []herdr.Issue{{ID: "SRE-2999"}}})
	m, cmd = drain(t, m, cmd) // → which repo? (the workspace exists)
	m, _ = drain(t, m, cmd)
	if m.repoPicker == nil || !m.repoFocusRow {
		t.Fatal("want a repo picker whose first row just opens the workspace")
	}
	m, cmd = enter(m)
	m, cmd = drain(t, m, cmd)
	if _, quit := cmd().(tea.QuitMsg); !quit || !slices.Contains(f.calls, "herdr workspace focus wB") {
		t.Errorf("calls = %q, want wB focused and agenda to exit", f.calls)
	}
}

func TestHerdrRequestIgnoredOutsideHerdrMode(t *testing.T) {
	m := newClickModel(&stubView{"Linear"})
	if _, cmd := m.Update(herdr.Request{Issues: []herdr.Issue{{ID: "SRE-1"}}}); cmd != nil {
		t.Error("a herdr request did something outside herdr mode")
	}
}
