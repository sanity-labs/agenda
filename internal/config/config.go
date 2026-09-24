// Package config loads agenda's user configuration from an XDG-compliant
// location. The tool ships with sensible defaults so it runs out of the box;
// personal details (a Linear API token, custom search filters) live in the
// config file rather than in code, keeping the binary generic and shareable.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the fully-resolved configuration (defaults merged with the file).
type Config struct {
	// Views lists which views to show, in tab order. Recognised names:
	// "prs", "sessions", "linear". Per-view `enabled: false` flags remove a
	// view from this list without editing it; see EnabledViews.
	Views []string `yaml:"views"`

	Theme   ThemeConfig   `yaml:"theme"`
	Refresh RefreshConfig `yaml:"refresh"`
	Notify  NotifyConfig  `yaml:"notifications"`

	// UpdateCheck asks GitHub once at startup whether a newer release
	// exists, and shows it in the tab bar. Default on; it never writes to
	// disk, it only reports. Set false to skip the network call entirely.
	UpdateCheck *bool `yaml:"update_check"`

	// Grouping renders lists as swimlanes derived from the active sort
	// (status lanes for Linear's status sort, time buckets for date sorts,
	// and so on). Off by default: flat lists, the original behavior. Sorts
	// with no feasible grouping stay flat either way.
	Grouping bool `yaml:"grouping"`

	// HidePreview starts with the preview (detail) pane hidden, leaving the
	// list full-width — friendlier to narrow terminals. Off by default; the
	// toggle_preview key (default "v") flips it at runtime either way.
	HidePreview bool `yaml:"hide_preview"`

	// Keys overrides key bindings: scope -> action -> keys. Scopes are
	// "global", "prs", "sessions", "linear"; actions and defaults are listed
	// in config.example.yml. A binding may be a single key or a list.
	Keys Keymap `yaml:"keys"`

	GitHub   GitHubConfig   `yaml:"github"`
	Linear   LinearConfig   `yaml:"linear"`
	Sessions SessionsConfig `yaml:"sessions"`
}

type ThemeConfig struct {
	// Name selects a built-in palette: "default", "catppuccin-mocha",
	// "catppuccin-latte", "tokyonight", "gruvbox", "dracula", "nord",
	// "rose-pine". Empty means "default" (the terminal's ANSI colors).
	Name string `yaml:"name"`
	// Palette overrides individual palette colors on top of the named theme.
	// Keys: accent, border, text, dim, green, red, yellow, blue, cyan,
	// magenta. Values are hex ("#89b4fa") or ANSI ("4").
	Palette map[string]string `yaml:"palette"`
	// Glyphs enables the decorative Nerd Font icons (tab and nav-tree
	// icons). Default true; the core status glyphs already assume a Nerd
	// Font, but this lets a plain-font setup drop the extras.
	Glyphs *bool `yaml:"glyphs"`
}

// UpdateCheckEnabled reports whether the startup release check runs.
func (c Config) UpdateCheckEnabled() bool { return c.UpdateCheck == nil || *c.UpdateCheck }

// GlyphsEnabled reports whether decorative Nerd Font icons render.
func (c Config) GlyphsEnabled() bool { return c.Theme.Glyphs == nil || *c.Theme.Glyphs }

// RefreshConfig controls background auto-refresh. Every is the global default
// interval; the per-view fields override it (set one to "0" to disable
// refresh for just that view). Zero/absent means no auto-refresh.
type RefreshConfig struct {
	Every    Duration  `yaml:"every"`
	PRs      *Duration `yaml:"prs"`
	Linear   *Duration `yaml:"linear"`
	Sessions *Duration `yaml:"sessions"`
}

// NotifyConfig controls notifications for newly-appeared items (a new PR
// waiting on your review, a new Linear issue assigned to you).
type NotifyConfig struct {
	// Popup picks the channel: "off" (default), "terminal" (an in-app
	// toast), or "desktop" (an OS notification).
	Popup string `yaml:"popup"`
	// Sound plays a sound alongside the popup (default true when on).
	Sound *bool `yaml:"sound"`
}

func (n NotifyConfig) Enabled() bool      { return n.Popup == "terminal" || n.Popup == "desktop" }
func (n NotifyConfig) SoundEnabled() bool { return n.Enabled() && (n.Sound == nil || *n.Sound) }

type GitHubConfig struct {
	// Enabled toggles the PRs view (default true).
	Enabled *bool `yaml:"enabled"`
	// Filter is the search query for your own PRs, in `gh search prs` syntax.
	Filter string `yaml:"filter"`
	// ReviewFilter is the search query for the "needs your review" section.
	ReviewFilter string `yaml:"review_filter"`
	// ShowReviewRequested shows the review-requested section on startup.
	// Off by default, matching the original single-search view; 'w'
	// toggles it in-app regardless.
	ShowReviewRequested *bool `yaml:"show_review_requested"`
	// DiffPane renders diffs in the preview pane on 'd'. Off by default:
	// 'd' then pages the diff through less, the original behavior.
	DiffPane bool `yaml:"diff_pane"`
	// MarkReviewed dims review-requested rows the viewer has already
	// reviewed and tags them "reviewed", so the eye can skip them. Off by
	// default.
	MarkReviewed bool `yaml:"mark_reviewed"`
}

type LinearConfig struct {
	// Enabled toggles the Linear view (default true; the view also hides
	// itself behind a setup hint when Token is empty).
	Enabled *bool `yaml:"enabled"`
	// Token is a Linear personal API key (lin_api_...). Required for the
	// Linear view; when empty the view renders a setup hint instead.
	Token string `yaml:"token"`
	// Filter narrows which issues are fetched. The default matches the
	// previous hardcoded behavior: your assigned issues that aren't
	// completed or canceled.
	Filter LinearFilter `yaml:"filter"`
	// Nav shows the navigation tree (My Issues / All Issues / pinned
	// projects) on startup. Off by default; ctrl+p toggles it either way.
	Nav bool `yaml:"nav"`
	// ShowComments renders issue comments in the preview on startup. Off
	// by default; 'c' toggles it either way.
	ShowComments bool `yaml:"show_comments"`
}

// LinearFilter mirrors the basic filter options in Linear's UI, applied
// server-side to the issue query.
type LinearFilter struct {
	legacyScalar bool // the pre-struct string form was found (and ignored)

	// Scope picks whose issues to fetch: "assigned" (yours, the default)
	// or "all" (every issue the token can see; combine with teams/projects/
	// states or the result is just the most recent 100).
	Scope            string   `yaml:"scope"`
	IncludeCompleted bool     `yaml:"include_completed"`
	IncludeCanceled  bool     `yaml:"include_canceled"`
	Teams            []string `yaml:"teams"`    // team keys, e.g. [SRE]
	Projects         []string `yaml:"projects"` // project names
	States           []string `yaml:"states"`   // workflow state names, e.g. [In Progress]
	Limit            int      `yaml:"limit"`    // max issues fetched (default 100, max 250)
}

// UnmarshalYAML tolerates the historical form of this key, a raw GraphQL
// clause string. That string was never consumed by any released code, so a
// config carrying it keeps working with default filtering instead of
// failing to load.
func (f *LinearFilter) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		f.legacyScalar = true
		return nil
	}
	type plain LinearFilter // avoid recursing into this method
	var pf plain
	if err := n.Decode(&pf); err != nil {
		return err
	}
	*f = LinearFilter(pf)
	return nil
}

type SessionsConfig struct {
	// Enabled toggles the sessions view. Defaults to true.
	Enabled *bool `yaml:"enabled"`
}

// Default returns the built-in configuration used when no file exists or to
// fill gaps in a partial file.
func Default() Config {
	return Config{
		Views: []string{"prs", "sessions", "linear"},
		GitHub: GitHubConfig{
			Filter:       "author:@me is:open archived:false",
			ReviewFilter: "review-requested:@me is:open archived:false",
		},
		Linear: LinearConfig{
			Filter: LinearFilter{Limit: 100},
		},
	}
}

// Dir is the directory agenda reads its config from:
// $XDG_CONFIG_HOME/agenda, falling back to ~/.config/agenda.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "agenda"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "agenda"), nil
}

// Path is the full path to the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yml"), nil
}

// Load reads the config file, merging it onto the defaults. A missing file is
// not an error — the defaults are returned and the file path is reported so a
// caller can offer to scaffold one.
func Load() (Config, error) {
	cfg := Default()

	path, err := Path()
	if err != nil {
		return cfg, err
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}

	// Unmarshal onto the defaults so absent keys keep their default value.
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	if cfg.Linear.Filter.Limit <= 0 {
		cfg.Linear.Filter.Limit = 100
	}
	// Linear rejects page sizes over 250 outright, which would make every
	// fetch fail; clamp rather than error.
	if cfg.Linear.Filter.Limit > 250 {
		cfg.Linear.Filter.Limit = 250
	}
	return cfg, nil
}

// SessionsEnabled reports whether the sessions view is on (default true).
func (c Config) SessionsEnabled() bool {
	return c.Sessions.Enabled == nil || *c.Sessions.Enabled
}

// viewEnabled resolves the per-view enabled flag for a view name.
func (c Config) viewEnabled(name string) bool {
	var flag *bool
	switch name {
	case "prs":
		flag = c.GitHub.Enabled
	case "linear":
		flag = c.Linear.Enabled
	case "sessions":
		flag = c.Sessions.Enabled
	default:
		return false
	}
	return flag == nil || *flag
}

// EnabledViews is the tab order (Views) with disabled views removed.
func (c Config) EnabledViews() []string {
	var out []string
	for _, name := range c.Views {
		if c.viewEnabled(name) {
			out = append(out, name)
		}
	}
	return out
}

// RefreshFor resolves the auto-refresh interval for a view name: the per-view
// override when set, else the global default. Zero disables auto-refresh.
func (c Config) RefreshFor(view string) time.Duration {
	var o *Duration
	switch view {
	case "prs":
		o = c.Refresh.PRs
	case "linear":
		o = c.Refresh.Linear
	case "sessions":
		o = c.Refresh.Sessions
	}
	if o != nil {
		return time.Duration(*o)
	}
	return time.Duration(c.Refresh.Every)
}

// ShowReviewRequested reports whether the PRs view starts with the
// review-requested section visible (default false, the original layout).
func (c Config) ShowReviewRequested() bool {
	f := c.GitHub.ShowReviewRequested
	return f != nil && *f
}

// --- yaml scalar types --------------------------------------------------------

// Duration is a time.Duration that unmarshals from strings like "5m" or "90s".
// "0", "off", and "" all mean disabled.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("line %d: duration must be a string like \"5m\"", n.Line)
	}
	switch s {
	case "", "0", "off", "none":
		*d = 0
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", n.Line, s)
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	if d == 0 {
		return "0", nil
	}
	return time.Duration(d).String(), nil
}

// Chord is one action's key list. It unmarshals from either a single scalar
// ("q") or a sequence (["q", "ctrl+c"]), so simple overrides stay simple.
type Chord []string

func (c *Chord) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*c = Chord{s}
		return nil
	}
	var ss []string
	if err := n.Decode(&ss); err != nil {
		return fmt.Errorf("line %d: keys must be a string or a list of strings", n.Line)
	}
	*c = Chord(ss)
	return nil
}

// Keymap is the user's keybind overrides: scope -> action -> keys.
type Keymap map[string]map[string]Chord

// Of returns the configured keys for scope/action, or def when unset.
// An explicitly-empty list disables the binding (returns an empty slice).
func (k Keymap) Of(scope, action string, def ...string) []string {
	if actions, ok := k[scope]; ok {
		if chord, ok := actions[action]; ok {
			return []string(chord)
		}
	}
	return def
}

// Has reports whether the user overrode scope/action at all (including an
// explicit empty list, which disables it).
func (k Keymap) Has(scope, action string) bool {
	actions, ok := k[scope]
	if !ok {
		return false
	}
	_, ok = actions[action]
	return ok
}
