// Package config loads agenda's user configuration from an XDG-compliant
// location. The tool ships with sensible defaults so it runs out of the box;
// personal details (a Linear API token, custom search filters) live in the
// config file rather than in code, keeping the binary generic and shareable.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the fully-resolved configuration (defaults merged with the file).
type Config struct {
	// Views lists which views to show, in tab order. Recognised names:
	// "prs", "reviews", "sessions", "linear". "reviews" is opt-in, so it is
	// not in the default list. Per-view `enabled: false` flags remove a
	// view from this list without editing it; see EnabledViews.
	Views []string `yaml:"views"`

	Theme   ThemeConfig   `yaml:"theme"`
	Refresh RefreshConfig `yaml:"refresh"`
	Notify  NotifyConfig  `yaml:"notifications"`

	// UpdateCheck asks GitHub once at startup whether a newer release
	// exists, and shows it in the tab bar. Default on; it never writes to
	// disk, it only reports. Set false to skip the network call entirely.
	UpdateCheck *bool `yaml:"update_check"`

	// Toggles decides how long a per-item view toggle lasts: "ephemeral"
	// (default) resets it when the selection moves, so opening a diff on one
	// PR does not put every other PR in diff view; "persist" keeps it until
	// you toggle back, which is what the views did before.
	Toggles string `yaml:"toggles"`

	// Unread marks rows that arrived since the last fetch with a dot, so a
	// notification you missed is still visible in the list. Selecting a row
	// clears its mark. On by default.
	Unread *bool `yaml:"unread"`

	// UnreadSync keeps unread marks and GitHub notifications in step, both
	// ways: reading a row here marks its notification read, and a
	// notification read anywhere else clears the mark here. Review-requested
	// PRs only, since your own PRs have no notification behind them. Off by
	// default: reading here is a cursor move, and that should not quietly
	// clear your real inbox.
	UnreadSync bool `yaml:"unread_sync"`

	// Footer shows the hotkey bar along the bottom. On by default; with it
	// off only the waiting-errors marker and the help key remain, on the
	// right, since those are the two you cannot work around by memory.
	Footer *bool `yaml:"footer"`

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

	// Unknown lists config keys that parsed but match nothing, so the app can
	// report them rather than silently ignoring a typo. Not a config key.
	Unknown []string `yaml:"-"`

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

// TogglesPersist reports whether a per-item toggle survives moving to
// another item. Only "persist" does; anything else, including the default,
// resets to the configured view.
func (c Config) TogglesPersist() bool { return c.Toggles == "persist" }

// UnreadEnabled reports whether new rows are marked unread.
func (c Config) UnreadEnabled() bool { return c.Unread == nil || *c.Unread }

// FooterEnabled reports whether the hotkey bar shows.
func (c Config) FooterEnabled() bool { return c.Footer == nil || *c.Footer }

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
	// Click is what a desktop notification does when clicked: "url" opens
	// the PR or issue (default), "none" does nothing. macOS needs
	// terminal-notifier for this; osascript notifications are not clickable.
	Click string `yaml:"click"`
}

func (n NotifyConfig) Enabled() bool { return n.Popup == "terminal" || n.Popup == "desktop" }

// ClickAction is the notification click behaviour, defaulting to opening the
// item.
func (n NotifyConfig) ClickAction() string {
	if n.Click == "none" {
		return "none"
	}
	return "url"
}

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
	// LazyPaging fetches PRs a page at a time, loading the next page when the
	// cursor reaches the end of the list. On by default: one page of 100 rows
	// of these fields measured 8-10s against a large review-requested search
	// and timed out often enough to matter, where a page of 20 is 3-4s.
	LazyPaging *bool `yaml:"lazy_paging"`
	// SummaryLines truncates the PR description in the preview to this many
	// lines, with 'e' expanding it (default 10). Set 0 to never truncate,
	// which is what the view did before.
	SummaryLines int `yaml:"summary_lines"`
	// PageSize is how many PRs one request asks for (default 20, max 100).
	// Larger pages mean fewer requests and a slower first paint.
	PageSize int `yaml:"page_size"`
	// MarkReviewed dims review-requested rows the viewer has already
	// reviewed and tags them "reviewed", so the eye can skip them. Off by
	// default.
	MarkReviewed bool `yaml:"mark_reviewed"`
	// Sort is the sort the view opens on: date, review, checks, repo, size
	// or author. Empty means the view's own default (date). Reverse flips
	// it, the same as pressing the reverse key at startup.
	Sort    string `yaml:"sort"`
	Reverse bool   `yaml:"reverse"`
	// HideApproved drops approved, still-open PRs from the
	// review-requested list: the ball is with the author. An approval by
	// anyone counts, not just the viewer's, since someone else approving
	// is no reason to stop looking. Off by default, and merged PRs are the
	// search filter's business (is:open), not this toggle's.
	HideApproved bool `yaml:"hide_approved"`
	// RefreshRow re-reads the selected PR once the cursor stops moving, so
	// check state and review decisions are current on the row you are
	// about to act on rather than as of the last full refresh. On by
	// default: it is one request for a single PR, debounced, so cycling a
	// list costs nothing until you stop.
	RefreshRow *bool `yaml:"refresh_row"`
	// Merge adds merge entries to the review popup ('r'). Off by default:
	// merging is the one irreversible action in that popup, so it is opt-in
	// rather than a keypress away for everyone.
	Merge bool `yaml:"merge"`
	// MergeMethod is how Merge merges: "squash" (default), "merge" or
	// "rebase". A repo may forbid the one you pick, which gh reports.
	MergeMethod string `yaml:"merge_method"`
	// MergeDeleteBranch deletes the head branch after a successful merge,
	// for repos that do not do it themselves. Off by default.
	MergeDeleteBranch bool `yaml:"merge_delete_branch"`
}

// RefreshRowEnabled reports whether the selected PR is re-read on settle.
func (g GitHubConfig) RefreshRowEnabled() bool { return g.RefreshRow == nil || *g.RefreshRow }

// ResolvedMergeMethod is the gh flag for the configured merge method,
// defaulting to squash. An unrecognised value falls back rather than
// failing: gh would reject a bad flag well after the confirmation.
func (g GitHubConfig) ResolvedMergeMethod() string {
	switch g.MergeMethod {
	case "merge", "rebase":
		return g.MergeMethod
	default:
		return "squash"
	}
}

// LazyPagingEnabled reports whether the PR search pages lazily.
func (g GitHubConfig) LazyPagingEnabled() bool { return g.LazyPaging == nil || *g.LazyPaging }

// ResolvedPageSize is the page size to request, clamped to what the GraphQL
// search accepts.
func (g GitHubConfig) ResolvedPageSize() int {
	if g.PageSize <= 0 {
		return 20
	}
	if g.PageSize > 100 {
		return 100 // the search API's own ceiling
	}
	return g.PageSize
}

type LinearConfig struct {
	// Enabled toggles the Linear view (default true; the view also hides
	// itself behind a setup hint when Token is empty).
	Enabled *bool `yaml:"enabled"`
	// Token is a Linear personal API key (lin_api_...). Required for the
	// Linear view; when empty the view renders a setup hint instead.
	Token string `yaml:"token"`
	// Sort is the sort the view opens on: date, status, project or
	// priority. Empty means the view's own default (date).
	Sort    string `yaml:"sort"`
	Reverse bool   `yaml:"reverse"`
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
	// Sort is the sort the view opens on: recent, cwd, tool, msgs or cost.
	// Empty means the view's own default (recent).
	Sort    string `yaml:"sort"`
	Reverse bool   `yaml:"reverse"`
}

// Sort vocabularies, one per view. Declared here because both the views
// (resolving a name to a mode) and the settings overlay (offering the
// choices) need them, and an empty value always means the view's default.
// The views own the meanings; a name dropped from one of these lists stops
// being selectable, and a name that no view knows falls back to default.
var (
	PRSortNames       = []string{"date", "review", "checks", "repo", "size", "author"}
	LinearSortNames   = []string{"date", "status", "project", "priority"}
	SessionsSortNames = []string{"recent", "cwd", "tool", "msgs", "cost"}
)

// Default returns the built-in configuration used when no file exists or to
// fill gaps in a partial file.
func Default() Config {
	return Config{
		Views: []string{"prs", "sessions", "linear"},
		GitHub: GitHubConfig{
			Filter:       "author:@me is:open archived:false",
			ReviewFilter: "review-requested:@me is:open archived:false",
			SummaryLines: 10,
		},
		Linear: LinearConfig{
			Filter: LinearFilter{Limit: 100},
		},
	}
}

// unknownKeys pulls the offending key names out of a yaml KnownFields error,
// whose message is a multi-line list of "line N: field X not found in type Y".
func unknownKeys(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		_, rest, ok := strings.Cut(line, "field ")
		if !ok {
			continue
		}
		name, _, ok := strings.Cut(rest, " not found")
		if ok {
			out = append(out, name)
		}
	}
	return out
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
	// KnownFields makes a key that does nothing an error rather than silence:
	// a typo or a renamed option is otherwise indistinguishable from the
	// feature not working.
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Keys that do nothing are worth reporting: a typo or a renamed option is
	// otherwise indistinguishable from the feature not working. A second
	// strict pass finds them without letting one stop agenda from starting.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var probe Config
	if err := dec.Decode(&probe); err != nil && !errors.Is(err, io.EOF) {
		cfg.Unknown = unknownKeys(err)
	}
	// A binding that claims a reserved key is dropped, so say so: the key
	// would otherwise keep its built-in meaning and the override would
	// look like it had simply not worked.
	cfg.Unknown = append(cfg.Unknown, cfg.Keys.reservedWarnings()...)
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
	case "prs", "reviews":
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
	case "prs", "reviews":
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
// ReservedKeys cannot be rebound: they are how you get out of any state,
// so a keymap that claims one is unrecoverable without editing the config
// by hand. Arrows move and change focus, esc steps back, ctrl+c quits.
// The key is fixed, not the action. Binding more keys to the same actions
// is fine and expected: hjkl alongside the arrows, or pgup/pgdown for
// paging. Only pointing a reserved key at something else is refused.
//
// Each entry names the scope.action the key is kept for, or "" when it is
// handled by the root model rather than a keymap action: focus, esc and
// ctrl+c are not keymap actions at all.
var ReservedKeys = map[string]struct{ Action, Why string }{
	"left":   {"", "move focus back to the list"},
	"right":  {"", "move focus into the pane"},
	"up":     {"list.up", "move within the focused pane"},
	"down":   {"list.down", "move within the focused pane"},
	"esc":    {"list.clear_filter", "step back out of whatever is open"},
	"ctrl+c": {"global.quit", "quit"},
}

// reservedWarnings names every binding that tried to claim a reserved key,
// for the startup message log.
func (k Keymap) reservedWarnings() []string {
	var out []string
	for scope, actions := range k {
		for action, chord := range actions {
			for _, key := range chord {
				if why, ok := ReservedFor(scope, action, key); !ok {
					out = append(out, fmt.Sprintf(
						"keys.%s.%s: %q is reserved (%s) and was ignored;"+
							" bind another key to this action instead",
						scope, action, key, why))
				}
			}
		}
	}
	sort.Strings(out) // map order is random; a stable message reads better
	return out
}

// Reserved reports whether a key is one agenda keeps for itself, and what
// it is kept for.
func Reserved(key string) (string, bool) {
	r, ok := ReservedKeys[normalKey(key)]
	return r.Why, ok
}

// ReservedFor reports whether binding key to scope.action is allowed. A
// reserved key may only be bound to the action it already means, so
// writing out a default (list.up: [up, k]) keeps working while pointing
// it elsewhere does not.
func ReservedFor(scope, action, key string) (string, bool) {
	r, ok := ReservedKeys[normalKey(key)]
	if !ok {
		return "", true // not reserved: bind it anywhere
	}
	if r.Action != "" && r.Action == scope+"."+action {
		return "", true // its own action, written out
	}
	return r.Why, false
}

func normalKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func (k Keymap) Of(scope, action string, def ...string) []string {
	if actions, ok := k[scope]; ok {
		if chord, ok := actions[action]; ok {
			// Drop any reserved key rather than the whole binding: the rest
			// of the override still works, and the user is told why in the
			// config warnings.
			kept := make([]string, 0, len(chord))
			for _, key := range chord {
				if _, ok := ReservedFor(scope, action, key); ok {
					kept = append(kept, key)
				}
			}
			return kept
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

// WithReviewsTab adds the reviews tab to a views list or takes it out. An
// empty list means the default views, so it is spelled out first rather
// than written back as a one-entry list that would drop the others. Added,
// it goes before prs: the point of the tab is to open on it.
func WithReviewsTab(views []string, on bool) []string {
	if len(views) == 0 {
		views = append([]string(nil), Default().Views...)
	}
	out := make([]string, 0, len(views)+1)
	for _, v := range views {
		if v == "reviews" {
			continue
		}
		if v == "prs" && on {
			out = append(out, "reviews")
		}
		out = append(out, v)
	}
	if on && !slices.Contains(out, "reviews") {
		out = append([]string{"reviews"}, out...)
	}
	return out
}
