package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// command is one `agenda <name>` subcommand. run returns the process exit
// code; a nil run means the command opens the TUI on that view.
type command struct {
	name, args, summary string
	run                 func(w io.Writer, args []string) int
}

func commands() []command {
	return []command{
		{name: "prs", summary: "open the TUI on the PRs view"},
		{name: "sessions", summary: "open the TUI on the Sessions view"},
		{name: "linear", summary: "open the TUI on the Linear view"},
		{name: "version", summary: "print the version", run: runVersion},
		{name: "update", summary: "check whether a newer release is available", run: runUpdate},
		{name: "completion", args: "<bash|zsh|fish>", summary: "print a shell completion script", run: runCompletion},
		{name: "help", args: "[command]", summary: "show this help", run: runHelp},
	}
}

// lookup finds a command by name. Views have no run func, so callers must
// check that separately.
func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func runVersion(w io.Writer, _ []string) int {
	fmt.Fprintln(w, "agenda", versionString())
	return 0
}

func runHelp(w io.Writer, args []string) int {
	if len(args) > 0 {
		c, ok := lookup(args[0])
		if !ok {
			fmt.Fprintf(w, "agenda: unknown command %q\n", args[0])
			return 1
		}
		fmt.Fprintf(w, "agenda %s %s\n    %s\n", c.name, c.args, c.summary)
		return 0
	}

	var b strings.Builder
	b.WriteString("agenda is a terminal dashboard for your PRs, agent sessions, and Linear issues.\n\n")
	b.WriteString("Usage:\n  agenda [command]\n\n")
	b.WriteString("Running agenda with no command opens the TUI on the first configured view.\n\n")
	b.WriteString("Commands:\n")
	width := 0
	for _, c := range commands() {
		if n := len(c.name + " " + c.args); n > width {
			width = n
		}
	}
	for _, c := range commands() {
		usage := strings.TrimSpace(c.name + " " + c.args)
		fmt.Fprintf(&b, "  %-*s  %s\n", width, usage, c.summary)
	}
	b.WriteString("\nConfig lives at $XDG_CONFIG_HOME/agenda/config.yml (default ~/.config/agenda/config.yml).\n")
	b.WriteString("Press ? inside the TUI for keybindings, or ctrl+s for the config overlay.\n")
	fmt.Fprint(w, b.String())
	return 0
}

func runCompletion(w io.Writer, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "agenda: completion needs a shell: bash, zsh, or fish")
		return 1
	}
	script, ok := completionScript(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "agenda: unsupported shell %q (want bash, zsh, or fish)\n", args[0])
		return 1
	}
	fmt.Fprint(w, script)
	return 0
}

// completionNames returns the completable subcommand names, in help order.
func completionNames() []string {
	names := make([]string, 0, len(commands()))
	for _, c := range commands() {
		names = append(names, c.name)
	}
	return names
}

func completionScript(shell string) (string, bool) {
	names := strings.Join(completionNames(), " ")
	switch shell {
	case "bash":
		return fmt.Sprintf(`# agenda completion for bash
# install: agenda completion bash > /usr/local/etc/bash_completion.d/agenda
_agenda() {
    local cur=${COMP_WORDS[COMP_CWORD]}
    case $COMP_CWORD in
        1) COMPREPLY=( $(compgen -W "%s" -- "$cur") ) ;;
        2)
            case ${COMP_WORDS[1]} in
                completion) COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ) ;;
                help) COMPREPLY=( $(compgen -W "%s" -- "$cur") ) ;;
            esac
            ;;
    esac
}
complete -F _agenda agenda
`, names, names), true

	case "zsh":
		var b strings.Builder
		b.WriteString("#compdef agenda\n")
		b.WriteString("# agenda completion for zsh\n")
		b.WriteString("# install: agenda completion zsh > \"${fpath[1]}/_agenda\"\n")
		b.WriteString("_agenda() {\n  local -a commands\n  commands=(\n")
		for _, c := range commands() {
			b.WriteString(fmt.Sprintf("    '%s:%s'\n", c.name, c.summary))
		}
		b.WriteString("  )\n")
		b.WriteString(`  _arguments -C '1: :->cmd' '*:: :->args'
  case $state in
    cmd) _describe -t commands 'agenda command' commands ;;
    args)
      case $words[1] in
        completion) _values 'shell' bash zsh fish ;;
        help) _describe -t commands 'agenda command' commands ;;
      esac
      ;;
  esac
}
_agenda "$@"
`)
		return b.String(), true

	case "fish":
		var b strings.Builder
		b.WriteString("# agenda completion for fish\n")
		b.WriteString("# install: agenda completion fish > ~/.config/fish/completions/agenda.fish\n")
		for _, c := range commands() {
			b.WriteString(fmt.Sprintf("complete -c agenda -n __fish_use_subcommand -a %s -d '%s'\n", c.name, c.summary))
		}
		b.WriteString("complete -c agenda -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'\n")
		return b.String(), true
	}
	return "", false
}
