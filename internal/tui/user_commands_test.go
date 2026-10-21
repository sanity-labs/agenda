package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

type fieldsView struct {
	fatView
	fields map[string]string
}

func (f *fieldsView) CommandFields() (string, map[string]string) { return "prs", f.fields }

func TestValidCommandsDropsDeadKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Commands = []config.Command{
		{Key: "O", View: "prs", Run: "true"},
		{Key: "esc", View: "prs", Run: "true"},  // reserved
		{Key: "c", View: "prs", Run: "true"},    // prs comments
		{Key: "q", View: "linear", Run: "true"}, // global quit
		{Key: "3", View: "prs", Run: "true"},    // view jump
		{Key: "O", View: "prs", Run: "true"},    // duplicate
		{Key: "O", View: "nope", Run: "true"},   // unknown view
		{Key: "O", View: "linear", Run: "true"}, // same key, other view: fine
	}
	ok, warn := validCommands(cfg)
	if len(ok) != 2 || len(warn) != 6 {
		t.Fatalf("kept %d, warned %d (%v); want 2 and 6", len(ok), len(warn), warn)
	}
}

// Row fields reach the command as environment, so a hostile branch name is
// data, never shell.
func TestCommandGetsFieldsAndChoice(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	cfg := config.Default()
	cfg.Commands = []config.Command{{
		Key: "O", View: "prs", Description: "open",
		Run: `printf '%s|%s' "$AGENDA_BRANCH" "$AGENDA_CHOICE" > ` + out,
	}}
	cfg.Commands[0].Prompt = &struct {
		Title   string   `yaml:"title"`
		Options []string `yaml:"options"`
	}{Options: []string{"pr", "main"}}
	branch := "x;touch " + out + ".pwned"
	m := escModel(t, true, &fieldsView{fatView: fatView{title: "PRs"}, fields: map[string]string{"branch": branch}})
	m.cfg, m.commands = cfg, cfg.Commands

	got, cmd := m.Update(press('O'))
	m = got.(Model)
	if m.pending == nil || cmd != nil {
		t.Fatal("O should open the prompt, not run yet")
	}
	got, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = got.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on the prompt should run the command")
	}
	if msg, ok := cmd().(ui.ToastMsg); !ok || !msg.Success {
		t.Fatalf("command failed: %+v", msg)
	}
	b, _ := os.ReadFile(out)
	if string(b) != branch+"|main" {
		t.Errorf("command saw %q, want %q", b, branch+"|main")
	}
	if _, err := os.Stat(out + ".pwned"); err == nil || !strings.Contains(err.Error(), "no such") {
		t.Error("branch name was run as shell")
	}
}
