# agenda

[![CI](https://github.com/sanity-labs/agenda/actions/workflows/ci.yml/badge.svg)](https://github.com/sanity-labs/agenda/actions/workflows/ci.yml)

A terminal dashboard that unifies the things you keep checking into one TUI you
tab between:

- **PRs** — your open GitHub pull requests
- **Sessions** — your local agent sessions (Claude Code, Codex, Antigravity),
  with the estimated cost and model for Claude sessions
- **Linear** — your assigned Linear issues

Each is a distinct *view*; switch with `tab` / `shift+tab`. Every view shares
the same two-line row layout, fuzzy filter, and a scrollable markdown preview.
Both the list and the preview show a slim scrollbar indicating your position
(the preview's appears only when its content overflows).

The network-backed views (PRs, Linear) cache their last results under
`$XDG_CACHE_HOME/agenda`, so they paint instantly on launch and refresh in the
background — you see your data immediately, not a loading spinner.

Built with [Bubble Tea v2](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss), and
[Glamour](https://github.com/charmbracelet/glamour).

## Credit: gh-dash

agenda's PR view — and much of its overall design — is **heavily inspired by
[gh-dash](https://github.com/dlvhdr/gh-dash)** by [Dolev Hadar](https://github.com/dlvhdr)
(MIT licensed). agenda doesn't vendor or copy gh-dash's code; it was built fresh
by studying gh-dash's source and reimplementing the ideas. Specifically, the
following are modeled on gh-dash:

- **The tabbed-views architecture** — a root model that hosts a slice of
  interchangeable views, each owning its own list, data fetch, and preview
  (gh-dash calls these "sections").
- **The two-line ("non-compact") row layout** — a dimmed metadata line over a
  bold title line, with the selection indicator spanning both.
- **Fetching PRs via the GitHub GraphQL API** rather than the search/REST JSON,
  so rows can show CI check rollup, review decision, diff size, comments, and
  mergeability — none of which `gh search prs --json` exposes.
- **The status-glyph vocabulary** — the state / CI / review Nerd Font icons.
- **Rendering issue/PR bodies with Glamour** in the preview pane.

If you work primarily inside a single repo and want the full-featured original,
use gh-dash. agenda's niche is unifying PRs *plus* local agent sessions *plus*
Linear in one switcher.

## Install

```sh
go install github.com/sanity-labs/agenda@latest
```

`agenda` opens on the first tab; `agenda prs` / `agenda sessions` /
`agenda linear` open straight on that view. `agenda help` lists every
command.

```sh
agenda help              # commands and where config lives
agenda version           # the running build
agenda update            # is a newer release out?
agenda completion zsh    # completion script (also bash, fish)
```

Shell completion, once per shell:

```sh
agenda completion zsh  > "${fpath[1]}/_agenda"          # zsh
agenda completion bash > /usr/local/etc/bash_completion.d/agenda
agenda completion fish > ~/.config/fish/completions/agenda.fish
```

### Working on agenda

```sh
go run .            # not `go run main.go`: the commands live in cli.go
go test ./...
```

### Updates

agenda checks GitHub once at startup for a newer release and shows it at the
right of the tab bar (`v0.1.2  ↑v0.2.0`); `agenda update` asks on demand and
prints the command that upgrades this particular install. It never replaces
the binary: whatever installed it (go install, Homebrew, a release archive)
owns that file. Set `update_check: false` to skip the network call.

Requirements:
- A **Nerd Font** in your terminal (for the status glyphs) — same as gh-dash.
- The **`gh` CLI**, authenticated (`gh auth login`) — powers the PRs view.

## Configuration

Config lives at `$XDG_CONFIG_HOME/agenda/config.yml` (defaults to
`~/.config/agenda/config.yml`). It's optional — agenda runs with sensible
defaults, and ships no personal details in the binary. See
[`config.example.yml`](./config.example.yml) for all options.

The only view that needs setup is **Linear**: add a personal API key
(linear.app → Settings → Security & access → API keys):

```yaml
linear:
  token: lin_api_xxx
```

## Keys

Every binding below is the default; all of them are remappable under `keys:`
in the config (see [`config.example.yml`](./config.example.yml)). `?` shows
the full keymap in-app.

| Scope | Key | Action |
|-------|-----|--------|
| Global | `tab` / `shift+tab` | switch view |
| Global | `1`…`9` | jump to a view by tab position |
| Global | `/` | fuzzy filter (all fields) |
| Global | `f` | field-scoped filter popup |
| Global | `j`/`k`, `g`/`G`, `ctrl+d`/`ctrl+u` | navigate list |
| Global | `shift+↑`/`shift+↓`, `PgUp`/`PgDn` | scroll preview |
| Global | `z` | zoom the preview pane to full width (tmux-style) |
| Global | `v` | show/hide the preview pane (nav-only, for narrow terminals) |
| Global | `l` | follow references — opens a picker of related items |
| Global | `ctrl+s` | config overlay (theme, refresh, notifications, views…) |
| Global | `ctrl+r` | refresh |
| Global | `?` | help: every binding for the focused view |
| Global | `q` | quit |
| PRs | `enter` · `y` · `s`/`S` | open · copy URL · cycle sort / reverse it |
| PRs | `d` | diff: `less` pager by default, right pane with `github.diff_pane` |
| PRs | `c` | toggle right pane to the comments/threads view |
| PRs | `w` | toggle the "Review Requested" section |
| PRs | `r` | review popup: approve / comment / request changes / view diff |
| PRs | `]` / `[` | jump between inline review threads |
| PRs | `R` · `X` · `C` | reply to thread · resolve thread · new PR comment |
| Sessions | `enter` · `s`/`S` | resume · cycle sort / reverse it |
| Linear | `enter` · `y` · `b` · `s`/`S` | open · copy URL · copy branch · cycle sort / reverse it |
| Linear | `ctrl+p`, then `←`/`→` | toggle the nav tree (Inbox / My Issues / All Issues / pinned projects); arrows move between tree and list |
| Linear | `m` | inside a project source: toggle only-mine vs everyone's issues |
| Linear | `c` | show comments and jump to them; press again to hide (jump-only when enabled via config) |
| Reference picker | `enter` · `o` · `esc` | follow · open in browser · cancel |
| Filter popup (`f`) | `↑`/`↓` (or `j`/`k`) · `space` · `enter` · `esc` | move · toggle field · apply · cancel |

While the filter (`/`) is open, typing refines the query; arrows, `ctrl+u`/`ctrl+d`,
and `home`/`end` still move the selection, so you can narrow and navigate at once.

`f` opens a popup to scope the filter to specific fields (contextual per view —
e.g. repo, branch, title, description for PRs) and toggle case sensitivity. One
cursor walks the whole popup: the query box at the top, then the field toggles,
then the case-sensitive row. `1`…`9` jump straight to a view by its tab position
(shown as a number prefix in each tab label).

## Configuration highlights

Everything below is opt-in, and every default matches the original behavior:
out of the box agenda looks and acts as it did before these options existed.

- **In-app config**: `ctrl+s` opens an overlay editable from anywhere: cycle
  the color theme live, edit refresh intervals, toggle notifications (with a
  test-notification button), views, and the Linear filter basics. An "edit
  keybinds" entry lists every binding grouped by scope and rebinds them on
  the fly with collision detection. Changes write back to `config.yml`
  without touching your comments.
- **Themes**: built-ins (`catppuccin-mocha`/`-latte`, `tokyonight`,
  `gruvbox`, `dracula`, `nord`, `rose-pine`) plus per-color overrides;
  `default` keeps your terminal's ANSI palette. Previews render markdown
  with heading icons, glyph checkboxes, readable inline code, and
  gutter-marked code blocks.
- **Auto-refresh**: a global `refresh.every` interval with per-view
  overrides.
- **Notifications**: when a PR newly requests your review or a Linear issue
  is newly assigned to you, get an in-app toast (`popup: terminal`) or an OS
  notification (`popup: desktop`), optionally with a sound. Bodies summarize
  the new items (`repo#N: title (@author)`).
- **Keybinds**: every action remappable per scope.
- **Update check**: `update_check` (default on) looks for a newer release at
  startup and flags it in the tab bar. Reports only, never self-updates.
- **Nav-only mode**: `v` hides the preview pane so the list takes the full
  width — `z`'s counterpart, for narrow terminals; `hide_preview: true`
  makes it the startup state.
- **Swimlanes**: `grouping: true` renders every view's list as sections
  derived from the active sort: status lanes for Linear's status sort,
  repo/review/checks/size lanes for PRs, cwd/tool lanes for sessions, and
  Today/Yesterday/7d/30d/Older buckets for date sorts. Sorts with no
  sensible buckets stay flat. `s`/`S` behave exactly as before; the lanes
  just follow whatever sort is active.

## Views

- **PRs** — fetched via `gh api graphql`. Shows state/CI/review glyphs, `+/−`
  diff size, comments, and labels; preview renders the description with Glamour.
  `s` cycles sort (date / review / checks / repo / size / author). `review` and `checks`
  are worst-first — changes requested and failing checks float to the top,
  approved and green sink to the bottom — and `size` puts the smallest diff
  first. All modes tie-break on recency. `w` adds PRs waiting on your review
  under a separator (`github.show_review_requested` makes that the default);
  with `github.mark_reviewed`, PRs there you've already reviewed render dim
  with a `reviewed` tag — instantly after an in-app review — so your eye
  skips them.
  `d` pages the diff through `less`; with `github.diff_pane` it renders in
  the right pane instead, with inline review threads pinned to the lines
  they discuss. `c` shows the full conversation. `r`/`a` review and approve
  via `gh`.
- **Sessions** — scans `~/.claude`, `~/.codex`, and `~/.gemini/antigravity-cli`,
  caching parsed metadata by file signature. Each agent is shown as a Nerd Font
  icon (claude = robot, codex = code, antigravity = rocket) rather than its
  name. `enter` resumes the selected session in its original directory; `s`
  cycles sort (recent / cwd / tool / msgs / cost). Originally a Python tool,
  ported to Go.
- **Linear**: issues assigned to you (active states by default; the fetch
  filter is configurable), via the Linear GraphQL API. Preview shows status,
  priority, labels, branch name, and the description. `s` cycles sort
  (date / status / project / priority); status orders by workflow state (in
  progress, then todo, triage, backlog) and breaks ties on priority, then
  recency. With `grouping: true`, each sort renders its matching swimlanes
  under styled section headers.

In every view `s` cycles the sort mode and `S` reverses whatever mode is active,
flipping the primary key and its tie-breaks together: `S` over `date` gives
oldest-first, over `size` biggest-first, over `checks` green-first. The active
mode shows in the list header (`12 PRs · sort: checks (rev)`).

### Why old sessions disappear

agenda applies no time window — it lists every transcript it finds. If your
history looks unexpectedly short, the files are gone: **Claude Code deletes
transcripts older than 30 days**, controlled by `cleanupPeriodDays`. To keep
them longer, set a large value in `~/.claude/settings.json`:

```json
{ "cleanupPeriodDays": 3650 }
```

This only stops future deletion; already-swept transcripts aren't recoverable.
Codex and Antigravity have their own retention behavior.

Sessions are dated by the last timestamp *inside* the transcript, not the file's
mtime, so a restore, sync, or migration that rewrites mtimes doesn't misdate a
session. Antigravity transcripts carry no timestamps, so those fall back to
mtime.

## Cross-references

Views link to each other and `l` follows the link, in every direction:

- **PR** → the Linear issue it references (from the title, branch, or body),
  shown with the issue's title on a second line.
- **Linear issue** → the GitHub PRs attached to it (each shown with its title
  and live state/CI/review icons) and the agent **sessions** that mention it.
- **Session** → the issues and PRs its conversation mentions (rendered like the
  other views — issue titles and PR status icons/titles from the store).
- **PR / issue** → the **sessions** that mention them, each with a dimmed line
  of context from the session.

A picker lists the targets (always, even for a single one, so navigation never
happens without a prompt), with issue/PR references grouped above a `sessions`
separator. `enter` follows the selection; `o` opens it in the browser (where it
has a URL). References that resolve to a loaded item jump in-app;
ones that don't (e.g. a merged PR, or a PR by someone else) open in the browser,
marked with `↗`. References that resolve to nothing — like regex false-positives
with no URL — are dropped.

### How it fits together

A small shared **metadata store** (`internal/store`) decouples the views: each
publishes the facts it owns — the PRs view publishes pull-request status, the
sessions view publishes which issues/PRs each session mentions — and any view
reads the others' to enrich its display. That's how the Linear view shows CI
icons for a PR (data the PRs view has) and lists the sessions referencing an
issue (data the sessions view has), without depending on those packages.

The cross-reference wiring itself is generic: a view exposes links by
implementing `Referencer`, and becomes a jump destination by implementing
`RefTarget`. Adding a new link type is just implementing those interfaces and,
if needed, publishing to the store — no changes to the core.

## Project layout

```
main.go                 loads config, wires the views, runs the program
internal/
  config/               XDG config loading
  cache/                generic on-disk JSON cache (instant startup)
  store/                shared metadata store the views publish to / read from
  ui/                   reusable widgets: list, picker, two-line rows,
                        scrollbar, glyphs, cross-reference builders
  tui/                  root model — tabs, layout, the picker, key routing
  views/
    prs/                GitHub pull requests (gh api graphql)
    sessions/           local agent sessions (JSONL scan + cache)
    linear/             Linear issues (GraphQL + token)
```

A view is anything implementing `tui.View`; it gains cross-references by also
implementing `ui.Referencer` / `ui.RefTarget`. The `tui` package never imports a
view package — `main` wires them in — so views stay decoupled and the store is
how they share data.

## License

MIT. See gh-dash's [MIT license](https://github.com/dlvhdr/gh-dash/blob/main/LICENSE.txt)
for the project whose ideas this builds on.
