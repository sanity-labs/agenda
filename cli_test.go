package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpListsEveryCommand(t *testing.T) {
	var b bytes.Buffer
	if code := runHelp(&b, nil); code != 0 {
		t.Fatalf("runHelp = %d, want 0", code)
	}
	for _, c := range commands() {
		if !strings.Contains(b.String(), c.name) {
			t.Errorf("help output missing %q:\n%s", c.name, b.String())
		}
	}
}

func TestHelpForUnknownCommandFails(t *testing.T) {
	var b bytes.Buffer
	if code := runHelp(&b, []string{"nope"}); code == 0 {
		t.Error("runHelp for an unknown command returned 0, want non-zero")
	}
}

// Every command in the registry must be completable, or the scripts drift
// from the dispatcher as commands are added.
func TestCompletionCoversEveryCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, ok := completionScript(shell)
		if !ok {
			t.Fatalf("completionScript(%q) not ok", shell)
		}
		for _, c := range commands() {
			if !strings.Contains(script, c.name) {
				t.Errorf("%s completion missing %q", shell, c.name)
			}
		}
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	if _, ok := completionScript("tcsh"); ok {
		t.Error("completionScript(tcsh) ok, want rejected")
	}
	var b bytes.Buffer
	if code := runCompletion(&b, []string{"tcsh"}); code == 0 {
		t.Error("runCompletion(tcsh) = 0, want non-zero")
	}
	if code := runCompletion(&b, nil); code == 0 {
		t.Error("runCompletion with no shell = 0, want non-zero")
	}
}

// The view names must stay in the registry: they are how `agenda prs` is
// discoverable, even though main dispatches them into the TUI, not to run.
func TestViewCommandsHaveNoRunFunc(t *testing.T) {
	for _, name := range []string{"prs", "sessions", "linear"} {
		c, ok := lookup(name)
		if !ok {
			t.Fatalf("view %q missing from the command registry", name)
		}
		if c.run != nil {
			t.Errorf("view %q has a run func; main must open the TUI instead", name)
		}
	}
}

// The zsh script is used two ways: autoloaded from fpath, where the file is
// the function and must run itself, and sourced, where it must only define
// the function (calling _arguments outside a completion errors). The guard
// on the call is what makes both work; an unguarded call breaks sourcing.
func TestZshCompletionOnlyDefinesWhenSourced(t *testing.T) {
	script, _ := completionScript("zsh")
	if !strings.Contains(script, `if [ "$funcstack[1]" = "_agenda" ]; then`) {
		t.Error("the zsh script calls _agenda without checking it is being autoloaded")
	}
	if !strings.Contains(script, "compdef _agenda agenda") {
		t.Error("the zsh script does not register itself for a sourced install")
	}
	if strings.Contains(script, "\n_agenda \"$@\"\n") {
		t.Error("the zsh script still calls _agenda unguarded at top level")
	}
}
