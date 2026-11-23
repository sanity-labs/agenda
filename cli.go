package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/sanity-labs/agenda/internal/tui"
	"github.com/sanity-labs/agenda/internal/ui"
)

// command is one `agenda <name>` subcommand. run returns the process exit
// code; a nil run means the command opens the TUI on that view.
type command struct {
	name, args, summary string
	run                 func(w io.Writer, args []string) int
	// define registers the command's flags on fs and returns a func that
	// turns the parsed result into the view to open.
	define func(fs *flag.FlagSet) func() (*tui.Open, error)
}

// extraCommands is appended by tests to exercise the generators.
var extraCommands []command

func commands() []command {
	return append([]command{
		{name: "prs", args: "[--repo [org/]repo [--pr N]]", summary: "open the TUI on the PRs view, on one PR, or on a repo's open PRs (org defaults to sanity-io)", define: definePRs},
		{name: "reviews", summary: "open the TUI on the Reviews view (when listed in views)"},
		{name: "sessions", summary: "open the TUI on the Sessions view"},
		{name: "linear", args: "[ISSUE-ID]", summary: "open the TUI on the Linear view, on one issue when given", define: defineLinear},
		{name: "daemon", summary: "run the background daemon in the foreground (the TUI starts one when daemon: true)", define: defineDaemon},
		{name: "status", summary: "show whether the daemon is running", run: runStatus},
		{name: "stop", summary: "stop the daemon", run: runStop},
		{name: "restart", summary: "stop the daemon and start a fresh one", run: runRestart},
		{name: "version", summary: "print the version", run: runVersion},
		{name: "update", summary: "check whether a newer release is available", run: runUpdate},
		{name: "completion", args: "<bash|zsh|fish>", summary: "print a shell completion script", run: runCompletion},
		{name: "help", args: "[command]", summary: "show this help", run: runHelp},
	}, extraCommands...)
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

// daemonArgError refuses arguments to `agenda daemon`, which takes none:
// ignored, "agenda daemon status" would try to start a second daemon.
func daemonArgError(args []string) string {
	if len(args) == 0 {
		return ""
	}
	msg := fmt.Sprintf("agenda daemon: unexpected argument %q", args[0])
	if _, ok := lookup(args[0]); ok {
		msg += fmt.Sprintf(" (did you mean agenda %s?)", args[0])
	}
	return msg
}

func defineDaemon(fs *flag.FlagSet) func() (*tui.Open, error) {
	return func() (*tui.Open, error) {
		if msg := daemonArgError(fs.Args()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, nil
	}
}

var issueID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-[0-9]+$`)

func defineLinear(fs *flag.FlagSet) func() (*tui.Open, error) {
	return func() (*tui.Open, error) {
		args := fs.Args()
		switch {
		case len(args) == 0:
			return nil, nil
		case len(args) > 1 || !issueID.MatchString(args[0]):
			return nil, fmt.Errorf("agenda linear: want an issue id like SRE-1234, got %q", strings.Join(args, " "))
		}
		return &tui.Open{Ref: ui.Ref{Kind: "linear", ID: strings.ToUpper(args[0])}}, nil
	}
}

func definePRs(fs *flag.FlagSet) func() (*tui.Open, error) {
	repo := fs.String("repo", "", "open this repo's PRs, as org/repo or a bare name (org defaults to sanity-io)")
	pr := fs.String("pr", "", "open this PR number zoomed (needs --repo)")
	return func() (*tui.Open, error) {
		if fs.NArg() > 0 {
			return nil, fmt.Errorf("agenda prs: unexpected argument %q", fs.Arg(0))
		}
		if *repo == "" {
			if *pr != "" {
				return nil, fmt.Errorf("agenda prs: --pr needs --repo")
			}
			return nil, nil
		}
		r := *repo
		if !strings.Contains(r, "/") {
			r = "sanity-io/" + r
		}
		if owner, name, _ := strings.Cut(r, "/"); owner == "" || name == "" || strings.Contains(name, "/") {
			return nil, fmt.Errorf("agenda prs: bad --repo %q, want [org/]repo", r)
		}
		if *pr == "" {
			return &tui.Open{Repo: r}, nil
		}
		n, err := strconv.Atoi(*pr)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("agenda prs: bad --pr %q, want a number", *pr)
		}
		url := fmt.Sprintf("https://github.com/%s/pull/%d", r, n)
		return &tui.Open{Ref: ui.Ref{Kind: "pr", ID: url, URL: url}}, nil
	}
}

// newFlagSet builds c's flag set. Its Usage is the single source of the
// command's help text, used by `<cmd> --help` and `agenda help <cmd>` alike.
func newFlagSet(c command, out io.Writer) (*flag.FlagSet, func() (*tui.Open, error)) {
	fs := flag.NewFlagSet("agenda "+c.name, flag.ContinueOnError)
	fs.SetOutput(out)
	var finish func() (*tui.Open, error)
	if c.define != nil {
		finish = c.define(fs)
	}
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage: agenda %s\n\n%s\n", strings.TrimSpace(c.name+" "+c.args), c.summary)
		n := 0
		fs.VisitAll(func(*flag.Flag) { n++ })
		if n > 0 {
			fmt.Fprint(out, "\nFlags:\n")
			fs.PrintDefaults()
		}
	}
	return fs, finish
}

func printUsage(w io.Writer, c command) {
	fs, _ := newFlagSet(c, w)
	fs.Usage()
}

// parseArgs parses the arguments after `agenda <c.name>`. It returns the
// view to open (nil to open as is) and the positional arguments left over.
// A -h or --help anywhere, even after a positional, yields flag.ErrHelp.
func parseArgs(c command, args []string) (*tui.Open, []string, error) {
	fs, finish := newFlagSet(c, io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("agenda %s: %w", c.name, err)
	}
	for _, a := range fs.Args() {
		if a == "-h" || a == "--help" {
			return nil, nil, flag.ErrHelp
		}
	}
	if finish == nil {
		return nil, fs.Args(), nil
	}
	open, err := finish()
	return open, fs.Args(), err
}

// argFailure reports a parseArgs error and returns the exit code: help goes
// to stdout and succeeds, anything else prints the error and usage to stderr.
func argFailure(c command, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		printUsage(os.Stdout, c)
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	fmt.Fprintln(os.Stderr)
	printUsage(os.Stderr, c)
	return 2
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
		printUsage(w, c)
		return 0
	}

	var b strings.Builder
	b.WriteString("agenda is a terminal dashboard for your PRs, agent sessions, and Linear issues.\n\n")
	b.WriteString("Usage:\n  agenda [command]\n\n")
	b.WriteString("Running agenda with no command opens the TUI on the first configured view.\n\n")
	b.WriteString("Flags:\n")
	b.WriteString("  --herdr  herdr mode, for a Herdr popup: selecting an issue, PR or session jumps to\n")
	b.WriteString("           its worktree workspace, creating it if needed, then agenda exits\n\n")
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

// zshQuote escapes s for the inside of a single-quoted zsh string.
func zshQuote(s string) string { return strings.ReplaceAll(s, "'", `'\''`) }

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
		b.WriteString("#      or: source <(agenda completion zsh)\n")
		b.WriteString("compdef _agenda agenda\n")
		b.WriteString("_agenda() {\n  local -a commands\n  commands=(\n")
		for _, c := range commands() {
			// A summary is quoted into the script, so a quote in one has
			// to be escaped or it ends the string and the rest will not
			// parse ("the daemon's state" did exactly that).
			fmt.Fprintf(&b, "    '%s:%s'\n", c.name, zshQuote(c.summary))
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
# Autoloaded from fpath, this file is the function, so run it. Sourced or
# eval-ed it must only define it: _arguments outside a completion errors.
if [ "$funcstack[1]" = "_agenda" ]; then
  _agenda "$@"
fi
`)
		return b.String(), true

	case "fish":
		var b strings.Builder
		b.WriteString("# agenda completion for fish\n")
		b.WriteString("# install: agenda completion fish > ~/.config/fish/completions/agenda.fish\n")
		for _, c := range commands() {
			fmt.Fprintf(&b, "complete -c agenda -n __fish_use_subcommand -a %s -d '%s'\n", c.name, strings.ReplaceAll(c.summary, "'", `\'`))
		}
		b.WriteString("complete -c agenda -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'\n")
		return b.String(), true
	}
	return "", false
}
