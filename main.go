// Command agenda is a terminal dashboard that unifies several "views" — your
// open GitHub PRs, your local agent sessions, and your Linear issues — into a
// single TUI you tab between. Configuration (including any personal details
// like a Linear API token) lives in $XDG_CONFIG_HOME/agenda/config.yml.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/notify"
	"github.com/sanity-labs/agenda/internal/store"
	"github.com/sanity-labs/agenda/internal/tui"
	"github.com/sanity-labs/agenda/internal/ui"
	"github.com/sanity-labs/agenda/internal/views/linear"
	"github.com/sanity-labs/agenda/internal/views/prs"
	"github.com/sanity-labs/agenda/internal/views/sessions"
)

func main() {
	// Subcommands run before config load, so a broken config never blocks
	// `agenda version` or `agenda help`.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			os.Exit(runVersion(os.Stdout, nil))
		case "--help", "-h":
			os.Exit(runHelp(os.Stdout, nil))
		}
		if c, ok := lookup(os.Args[1]); ok && c.run != nil {
			os.Exit(c.run(os.Stdout, os.Args[2:]))
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agenda: config error:", err)
		os.Exit(1)
	}

	// The theme applies process-wide, so resolve it before any view exists.
	palette, err := ui.ResolvePalette(cfg.Theme.Name, cfg.Theme.Palette)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agenda: config error:", err)
		os.Exit(1)
	}
	ui.SetPalette(palette)
	ui.SetGlyphs(cfg.GlyphsEnabled())

	// Shared metadata store: views publish facts they own (PR status, session
	// mentions) and read each other's to render cross-references.
	st := store.New()

	// nil when notifications are off; views treat that as disabled.
	notifier := notify.New(cfg.Notify.Popup, cfg.Notify.SoundEnabled(), cfg.Notify.ClickAction())

	// Build the configured views in tab order (disabled views drop out).
	enabled := cfg.EnabledViews()
	var views []tui.View
	for _, name := range enabled {
		switch name {
		case "prs":
			pv := prs.New(cfg.GitHub, cfg.Keys, prsNotifier(enabled, notifier), st)
			if hasReviewsTab(enabled) {
				pv.DelegateReviews()
			}
			views = append(views, pv)
		case "reviews":
			views = append(views, prs.NewReviews(cfg.GitHub, cfg.Keys, notifier, st))
		case "sessions":
			views = append(views, sessions.New(cfg.Sessions, cfg.Keys, st))
		case "linear":
			views = append(views, linear.New(cfg.Linear, cfg.Keys, notifier, st))
		}
	}

	// `agenda linear` (or prs/sessions) opens on that view.
	initial := 0
	if len(os.Args) > 1 {
		want := strings.ToLower(os.Args[1])
		found := false
		for i, name := range enabled {
			if name == want {
				initial, found = i, true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "agenda: unknown or disabled view %q (enabled: %s)\ntry 'agenda help'\n",
				os.Args[1], strings.Join(enabled, ", "))
			os.Exit(1)
		}
	}

	// A reload execs the binary with a note of where it left off; read and
	// clear it so a later manual launch does not reopen the settings.
	reloaded := os.Getenv(reloadEnv)
	os.Unsetenv(reloadEnv)
	p := tea.NewProgram(tui.New(cfg, views).WithVersion(versionString()).WithInitialView(initial).WithReloaded(reloaded))
	final, err := p.Run()
	if err == nil {
		if m, ok := final.(tui.Model); ok && m.Restart() {
			// The settings overlay asked for a reload. Exec rather than
			// loop: it is a full restart, so every option takes effect the
			// way it would after quitting and running agenda again.
			exe, err := os.Executable()
			if err == nil {
				err = syscall.Exec(exe, os.Args, append(os.Environ(), reloadEnv+"="+m.RestartState()))
			}
			fmt.Fprintln(os.Stderr, "reload failed:", err)
			os.Exit(1)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agenda:", err)
		os.Exit(1)
	}
}

// prsNotifier is the PRs tab's notifier. A Reviews tab watches the same
// review-requested search, so when one is open it owns those notifications
// and the PRs tab stays quiet rather than popping each one twice.
func prsNotifier(enabled []string, n notify.Notifier) notify.Notifier {
	if hasReviewsTab(enabled) {
		return nil
	}
	return n
}

func hasReviewsTab(enabled []string) bool {
	for _, name := range enabled {
		if name == "reviews" {
			return true
		}
	}
	return false
}

// reloadEnv carries the settings panel's state across a reload.
const reloadEnv = "AGENDA_RELOAD"

// Set by GoReleaser via -ldflags "-X main.version=..." on release builds.
// go-install and source builds leave them empty and fall back to the
// module/VCS metadata the Go toolchain embeds on its own.
var (
	version string
	commit  string
	date    string
)

func versionString() string {
	if version != "" {
		out := version
		if commit != "" {
			out += " (" + commit
			if date != "" {
				out += ", " + date
			}
			out += ")"
		}
		return out
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v // go install: tag or pseudo-version resolved by the module proxy
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return "devel (" + rev + ")"
}
