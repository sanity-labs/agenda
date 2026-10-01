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

The PR view's jobs pane (`t`) is modeled on gh-dash's companion
[gh-enhance](https://github.com/dlvhdr/gh-enhance) (also MIT), the same way:
a PR's check runs read from the head commit's rollup and grouped by workflow
run, with rerun through `gh`. gh-enhance goes further, with parsed and
searchable logs in-app; agenda pages the log through `less` instead.

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

## Releasing

Merges to `main` accumulate into a **draft** release, which only maintainers
can see. Publishing that draft creates the `vX.Y.Z` tag, and the tag triggers
GoReleaser to build the binaries and attach them. Nothing ships until someone
publishes the draft.

The version and the changelog both come from PR labels, applied automatically:

- The conventional-commit prefix on the **PR title** sets the type label
  (`feat:` → `feature`, `fix:` → `fix`, `perf:`, `docs:`, `ci:`, `chore:`),
  applied by the `autolabel` job on every PR event.
- `feature` bumps the minor version, `breaking` the major, anything else the
  patch.
- Changed paths add area labels (`tui`, `prs-view`, `config`, ...) for triage.
  These never decide a changelog category, so a PR appears exactly once.

So a PR titled `fix(tui): …` lands under Fixes with a patch bump, with no
manual labelling. If the notes look wrong, check the PR's labels first.

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
| Global | `!` | message log: fetch failures, stale data, actions |
| Global | `?` | help: every binding for the focused view |
| Global | `q` | quit |
| PRs | `enter` · `y` · `s`/`S` | open · copy URL · cycle sort / reverse it |
| PRs | `d` | diff: `less` pager by default, right pane with `github.diff_pane` |
| PRs | `c` | toggle right pane to the comments/threads view |
| PRs | `w` | toggle the "Review Requested" section |
| PRs | `e` | expand a truncated description |
| PRs | `r` | review popup: approve / comment / request changes / view diff |
| PRs | `]` / `[` | jump between inline review threads (between failed jobs in the jobs pane) |
| PRs | `t` | toggle right pane to the PR's CI jobs, grouped by workflow, and focus it |
| Jobs pane | `j`/`k`, `g`/`G`, `ctrl+d`/`ctrl+u` | move between jobs and their steps |
| Jobs pane | `enter` · `→` / `←` | open a job into its steps, or a step's log in the pane · into / out of a job |
| Jobs pane | `o` · `p` · `y` · `x` | open in browser · page the log through `less` · copy URL · rerun popup |
| Jobs pane | `esc` | hand the keys back to the PR list (`→` or a click takes them again) |
| Step log | `j`/`k`, `ctrl+d`/`ctrl+u`, `g`/`G` · `]` / `[` · `esc` | scroll · next / previous error · back to the jobs |
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

The mouse works too: click a tab to switch view, click a row to select it, and
double-click a row to run its `enter` action (open, or resume a session). In
the Linear nav tree a click switches the source. The wheel scrolls whichever
pane it's over: the list one row per notch, the preview as many lines as your
terminal sends for a notch (three in Ghostty), and trackpad swipes scroll with
their speed. Mouse capture means selecting text needs your terminal's bypass
modifier (usually `shift`, or `option` in iTerm2).

### Messages

A status row above the footer reports things that affect what you are looking
at: a fetch that partly failed, cached data shown because a refresh failed, a
completed action. `!` opens the log with the full text and recent history, and
an error opens it on arrival, since the detail usually says how to fix it.

One case worth naming: if `GITHUB_TOKEN` is set in your environment, `gh`
prefers it over its own login, and an org that forbids classic tokens then
returns your PRs as nulls. agenda shows the PRs it could read and says how
many it could not, rather than dropping them silently.

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
- **Lazy paging**: the PRs view fetches one page (`github.page_size`, 20 by
  default) and loads the next when you scroll to the end, so a large
  review-requested search paints in seconds instead of timing out. The list
  header says `20 of 79` while more remain. `github.lazy_paging: false`
  fetches everything up front.
- **PR preview**: the description is trimmed to `github.summary_lines` (10 by
  default, `e` expands, 0 never trims), framed by a checks box that says what
  is blocking the merge and how many checks passed, and a comments line
  saying how many there are and which key opens them.
- **Toggle behavior**: `toggles: ephemeral` (the default) treats a per-item
  toggle as belonging to the item you pressed it on, so the diff pane,
  an expanded description or shown comments reset when you move on, and the
  pane you opened to review a PR folds away once you have reviewed it.
  `toggles: persist` keeps them until you toggle back.
- **Floating detail**: with the preview pane off (`hide_preview: true`),
  `v` opens the detail as a centered window over the list rather than
  splitting the pane; `v` again, or moving to another row, closes it. With
  the pane on, `v` keeps its original meaning and hides or shows the pane.
- **Notification clicks**: a desktop notification opens the PR or issue when
  clicked (`notifications.click: none` turns that off). On macOS this needs
  `brew install terminal-notifier`, which also gives the notification a
  proper icon rather than Script Editor's; without it agenda falls back to
  `osascript`, which posts but cannot be clicked.
- **Unread marks**: a row that arrived since the last fetch carries a blue
  dot until you have seen it, so a notification you missed is still visible
  at a glance. Marks persist across restarts: quitting does not mark
  anything read. What counts as reading depends on the detail pane. With it
  on screen, moving onto a row reads it; with it hidden (`hide_preview`),
  the row is read when you ask for the detail with `v`. Independent of
  whether notifications are on; `unread: false` turns it off, and
  `unread_sync: true` keeps marks and GitHub notifications in step both
  ways (review-requested PRs only, one extra request per refresh).
- **Floating detail**: with the preview pane off, `v` opens the detail as a
  centered window over the list rather than splitting the pane, and moving
  to another row closes it. `z` still zooms the pane when one is in view.
- **Hide approved**: `github.hide_approved: true` drops approved,
  still-open PRs from the review-requested list, since the ball is with
  the author. An approval by anyone counts, not just yours, and the
  toggle is there for when you still want to look (to comment, say).
  Merged PRs are `review_filter`'s business (`is:open`), not this.
- **Fresher rows**: the selected PR is re-read once the cursor stops
  moving, so checks that finished on GitHub and reviews left since the
  last refresh show up without waiting for the refresh timer. Debounced,
  and one PR rather than the whole search, so cycling a list fires
  nothing until you stop. `github.refresh_row: false` turns it off.
  Approving a PR you have already reviewed asks first, rather than
  stacking a second approval on the first.
- **Merge from the popup**: with `github.merge: true`, the review popup
  ('r') gains `m` to merge and `M` to enable auto-merge. It always
  confirms first, naming the PR and the method, and refuses a draft, a
  conflict or a pending mergeability check rather than failing at the
  `gh` call. An unapproved or failing PR warns but still asks: the repo's
  own rules are what gate the merge. `github.merge_method` picks squash
  (default), merge or rebase, and `github.merge_delete_branch` cleans up
  the head branch.
- **Startup sort**: each view's opening sort is configurable, since `s`
  cycling back to the default on every launch meant re-pressing it every
  time. `github.sort` (date, review, checks, repo, size, author),
  `linear.sort` (date, status, project, priority) and `sessions.sort`
  (recent, cwd, tool, msgs, cost), each with a `reverse` companion. Omit
  for the view's own default; `s` and `S` behave exactly as before at
  runtime.
- **Label column**: with the preview pane off, a wide enough list shows
  labels between the metadata and the right-hand cells, in a fixed column
  so they line up down the list. In both the PRs and Linear views. Labels
  that do not fit collapse to a `+2` rather than being dropped silently.
- **Stable columns**: the diff, comment and age cells are padded to fixed
  widths and left-aligned, so they read as columns instead of shifting
  with their contents. Large counts switch unit (`+1.2k -567`) rather than
  widening the column.
- **Settings tabs**: `ctrl+s` opens the settings in six tabbed sections
  (general, appearance, alerts, prs, linear, sessions) rather than one
  long scroll;
  `tab`/`shift+tab` moves between them, and the mouse works too: a click
  on a tab switches, a click on a row toggles or cycles it, a click
  outside closes. A test asserts every section belongs to exactly one tab,
  so a setting cannot become unreachable.
- **GitHub-style filtering**: the in-app filter takes qualified terms as
  well as fuzzy words, matched against the rows already loaded, so no
  refetch: `-label:dependencies`, `is:draft`, `review:approved`,
  `checks:failing`, `author:x`, `repo:y`, quoted values for labels with
  spaces. Terms are ANDed and a leading `-` excludes. A bare word still
  matches fuzzily across the visible fields.
- **Edit the search**: `F` opens the GitHub search query for the section
  the cursor is in (your PRs, or review requests), prefilled. Enter tries
  it and writes it to config only once it returns something; esc cancels.
  A filter that matches nothing *only* because of an author term is
  rejected with a warning and the previous one restored: GitHub resolves
  `author:` against real accounts and answers a name it cannot find with
  a clean zero, so a typo looks exactly like "nothing matches". Bots are
  `app/<name>`, e.g. `app/renovate`. Both filters are also editable rows
  in the prs settings tab, with a reset action that puts them back to the
  defaults.
- **Effective query**: each section's band names the search that produced
  it, so a missing PR can be explained from the screen. The counts keep
  their room and the query truncates, since the counts are what the band
  is for.
- **Hotkey bar**: `footer: false` hides it, leaving a waiting-errors
  marker and the help key on the right. Warnings and errors no longer
  hold a permanent row: the toast announces one and the footer says it is
  waiting, so `!` opens the log when you want it.
- **File list**: `D` shows the diff as a list of files with their own
  counts, GitHub-style: `+`/`→` expands one file's hunks inline, `-`/`←`
  collapses, `space` marks a file reviewed and moves to the next, and the
  summary counts progress. It is also the only diff view that works on a
  large PR: the unified diff endpoint refuses past 300 files with a 406,
  while the files endpoint pages happily (937 files tested).
- **Pane focus**: right arrow puts the keys in the preview pane, left
  gives them back to the list, and the list dims while they are away so
  only one cursor is lit. Works for every pane that has something to
  navigate or scroll: jobs, diffs, comments, and Linear's comments. In
  Linear the nav tree keeps the left arrow, so the preview takes the
  right. Arrows and page keys move within the focused pane.
- **esc closes things**: one rule across the app, innermost first. A job
  log, then the jobs pane, then pane focus, then the pane itself, then a
  floating detail, then zoom. It never closes agenda; that is `q` alone, and a
  preview pane you configured on stays, since esc only undoes what was
  opened over it.
- **Keybinds**: every action remappable per scope.
- **Update check**: `update_check` (default on) looks for a newer release at
  startup and flags it in the tab bar. Reports only, never self-updates.
- **Nav-only mode**: `v` hides the preview pane so the list takes the full
  width — `z`'s counterpart, for narrow terminals. With the pane on this
  is a peek: moving to another row brings it back. `hide_preview: true`
  makes hidden the startup state, and there `v` floats one row's detail
  instead.
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
  `t` shows the PR's CI jobs: every check run and commit status on the head
  commit, grouped by workflow run with the worst first, failed steps listed
  under each failed job, and durations. The pane takes the keys while it is
  open, which the chrome shows: the PR list greys out except the PR the
  pane is showing, the pane's border lights up, and the footer leads with
  `JOBS` and the pane's own keys.
  `j`/`k` walk jobs and steps, `enter` opens a job into all its steps (`←`
  closes it again, `←` once more or `esc` goes back to the PR list), and
  `]`/`[` jump between failures. A job's marker says what `enter` does: `▸`
  opens it into steps, `↗` leaves for an external check's own page. Once a
  job's steps are on screen its log is fetched, so each step says how many
  lines it printed; a step that printed nothing, or was skipped, is faint.
  `enter` on a step shows its log in the pane, cleaned of timestamps and
  colour codes, grouped and coloured by GitHub's markers, and opened at the
  first error; `]`/`[` move between errors and `esc` goes back. `p` pages
  the same log through `less` instead, for searching. `o` opens a job or
  step in the browser (a step at its own anchor) and `y` copies that URL.
  `x` reruns: the failed jobs, the job under the cursor, or its whole
  workflow, once the run has finished. A fetch GitHub answers with an error
  page (a transient 5xx) is retried before it is reported; `ctrl+r` retries
  from the pane. While anything is running the pane refetches every 10s, and
  when the last job finishes the row's CI state is re-read.
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
