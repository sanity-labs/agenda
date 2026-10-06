# agenda

[![CI](https://github.com/sanity-labs/agenda/actions/workflows/ci.yml/badge.svg)](https://github.com/sanity-labs/agenda/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/sanity-labs/agenda)](https://github.com/sanity-labs/agenda/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A terminal dashboard for the things you keep checking, in one TUI you tab
between:

- **PRs**: your open GitHub pull requests, with review requests as a section
  or as their own **Reviews** tab
- **Sessions**: your local agent sessions (Claude Code, Codex, Antigravity),
  with model and estimated cost for Claude
- **Linear**: your assigned Linear issues, plus an inbox and project tree

Every view has the same two-line rows, the same filter, and a markdown
preview beside the list (or floating over it). Network views cache their
last result under `$XDG_CACHE_HOME/agenda`, so the app paints at once and
refreshes behind you.

You need a **Nerd Font** in the terminal, the **`gh` CLI** logged in for the
PRs view, and a Linear API key for the Linear view.

**Contents**: [Install](#install) · [Configuration](#configuration) ·
[Keys](#keys) · [Views](#views) · [Cross-references](#cross-references) ·
[Releasing](#releasing) · [Credit](#credit) · [License](#license)

## Install

```sh
go install github.com/sanity-labs/agenda@latest
```

`agenda` opens on the first tab; `agenda prs`, `agenda reviews`,
`agenda sessions`, `agenda linear` open on that view. `agenda help` lists the
commands, `agenda version` prints the build, `agenda update` says whether a
newer release exists and how to get it for this install (it never replaces
the binary itself). The same check runs once at startup and shows in the tab
bar; `update_check: false` turns it off.

Shell completion, once per shell:

```sh
agenda completion zsh  > "${fpath[1]}/_agenda"
agenda completion bash > /usr/local/etc/bash_completion.d/agenda
agenda completion fish > ~/.config/fish/completions/agenda.fish
```

To hack on it: `make help` lists the targets; `make run`, `make test`, and
`make check` (formatting, vet, tests: what CI runs). `go run .` works too
(the commands live in `cli.go`, so not `main.go`). [AGENTS.md](AGENTS.md)
and [DESIGN.md](DESIGN.md) hold the conventions and the interaction rules.

## Configuration

`$XDG_CONFIG_HOME/agenda/config.yml` (default `~/.config/agenda/config.yml`),
optional. [`config.example.yml`](./config.example.yml) documents every key.
`ctrl+s` edits all of them in-app, grouped in tabs, and writes back to the
file without touching your comments; it also has a keybind editor. Options
marked `(reload required)` offer `r` in the overlay to reload agenda in place;
leaving without reloading warns once, and `esc` then reverts them.

The one thing that needs setup is Linear:

```yaml
linear:
  token: lin_api_xxx     # linear.app → Settings → Security & access → API keys
```

Review requests show as a section under your own PRs (`w` hides it). To
work through them on their own, give them a tab by enabling the `reviews`
view. The order of `views` is the tab order, so the first one is what
agenda opens on:

```yaml
views: [reviews, prs, sessions, linear]   # opens on Reviews
github:
  review_filter: review-requested:@me is:open archived:false
  hide_approved: true           # someone already approved: the ball is with the author
  hide_dependency_bots: true    # leave Renovate and Dependabot out of the search
  mark_reviewed: true           # dim what you have already reviewed
notifications:
  popup: desktop                # a review request arrives: say so
```

With the Reviews tab on, it owns the review search; the PRs tab shows your
own PRs only and `w` says so.

<details>
<summary>Options people reach for most (all off unless noted)</summary>

| Key | Does |
|---|---|
| `hide_preview` | start with the list full width; `v` floats one row's detail |
| `github.diff_pane` | `d` shows the diff in the pane as a file list instead of paging it through `less` |
| `github.review_view` | what `r` opens beside the review popup: `files` (default) or `unified` |
| `github.hide_drafts` | leave draft PRs out of both sections; `D` toggles in-app |
| `grouping` | swimlanes under the active sort (status, repo, Today/Yesterday/…) |
| `unread`, `unread_sync` | blue dot on rows that arrived since you last looked (on); keep it in step with GitHub notifications |
| `notifications.popup` | `terminal` toast or `desktop` notification on new review requests and assignments; `click` opens the item (macOS needs `terminal-notifier`) |
| `refresh.every` | auto-refresh interval, with per-view overrides |
| `list_jump` | rows `pgup`/`pgdn` and `shift+↑↓` move (5) |
| `github.merge` | `m`/`M` in the review popup merge or enable auto-merge, after confirming |
| `github.sort`, `linear.sort`, `sessions.sort` | the sort each view opens on |
| `theme.name` | `catppuccin-mocha`, `tokyonight`, `gruvbox`, `dracula`, `nord`, `rose-pine`, … |
| `toggles: persist` | keep `d`/`c`/`t` and an expanded description across rows instead of per row |
| `footer: false` | hide the hotkey bar |

</details>

Every key binding is remappable per scope under `keys:`. Arrows, `esc` and
`ctrl+c` keep their meaning (you can add keys to those actions, not point
them elsewhere).

## Keys

`?` shows the bindings for whatever has focus.

<details>
<summary>The defaults</summary>

| Scope | Key | Action |
|---|---|---|
| Global | `tab` / `shift+tab`, `1`…`9` | switch view |
| Global | `/`, `f` | filter the loaded rows (fuzzy on word starts, plus GitHub-style terms like `-label:x is:draft`); field-scoped filter popup. `esc` clears it |
| Global | `→` / `←` | give the keys to the pane / back to the list; in a float, `←` steps a level out |
| Global | `esc` | step back: focus, then the pane, then a float, then zoom. Never quits |
| Global | `v`, `z` | show/hide (or float) the preview; zoom it |
| Global | `l` | follow a cross-reference via a picker; inside a focused issue detail it expands the pull requests instead |
| Global | `ctrl+s`, `ctrl+r`, `!`, `q` | settings, refresh, message log, quit (closes an open pane or float first; `ctrl+c` quits outright) |
| List | `j`/`k`, `g`/`G`, `ctrl+u`/`ctrl+d`, `pgup`/`pgdn` or `shift+↑↓` | move, top/bottom, half page, jump `list_jump` rows |
| PRs | `enter`, `y`, `s`/`S` | open, copy URL, cycle sort / reverse |
| PRs | `d`, `c`, `t`, `e` | diff (file list), comments, CI jobs, expand description |
| PRs | `r` | review popup: approve, comment, request changes, view diff; `m`/`M` merge with `github.merge` |
| PRs | `R`, `X`, `C`, `]`/`[` | reply to thread, resolve thread, new comment, next/previous thread |
| PRs | `w`, `D`, `F` | toggle the review section; hide drafts; edit the section's GitHub search |
| File list | `↑`/`↓`, `+`/`→`, `-`/`←`, `space` | next file when on screen (else scroll a line), expand, collapse, mark reviewed and advance; jump keys scroll |
| Jobs pane | `enter`, `→`/`←`, `]`/`[`, `o`, `p`, `y`, `x` | open a job or a step's log, in/out of a job, next/previous failure, browser, `less`, copy URL, rerun |
| Sessions | `enter`, `y`, `s`/`S` | resume in its directory, copy the transcript path, sort |
| Linear | `enter`, `y`, `b`, `s`/`S` | open, copy URL, copy branch, sort |
| Linear | `ctrl+p`, `c`, `m` | project tree (show and focus; again hides), comments, only-mine in a project |

</details>

The mouse works: click tabs, rows, the tree and the hints in the preview;
double-click runs `enter`; the wheel scrolls the pane it is over. Selecting
text needs your terminal's bypass modifier (`shift`, or `option` in iTerm2).

A pane that has the keys dims the list, so one cursor is lit. A float
(`hide_preview`) has levels: `v` is the description, `c`/`d`/`t` over it is
level two, `←` steps down and `esc` closes the whole thing. The rules are
written down in [DESIGN.md](DESIGN.md).

## Views

**PRs** fetches through `gh api graphql`, one page at a time
(`github.page_size`, 20) and the next as you reach the end. Rows carry state,
CI and review glyphs, diff size, comments, labels and age in fixed columns.
Each section's band names its search and count. Sorts: date, review, checks
(worst first), repo, size, author. `d` lists the changed files with their
counts and expands them inline, with review threads boxed under the lines
they anchor to; it is the one diff view that works past GitHub's 300-file
limit. `t` lists the head commit's check runs grouped by workflow, worst
first: open a job for its steps, a step for its log (cleaned, opened at the
first error), rerun from the pane, and it polls while anything runs. The
selected PR is re-read when the cursor settles, so a check that just
finished shows without waiting for the timer.

**Sessions** scans `~/.claude`, `~/.codex` and `~/.gemini/antigravity-cli`,
parsing only files whose signature changed. Sorts: recent, cwd, tool, msgs,
cost. Sessions are dated by the last timestamp inside the transcript, not
the file's mtime. If history looks short, the files are gone: Claude Code
deletes transcripts after 30 days unless `~/.claude/settings.json` says
otherwise (`{ "cleanupPeriodDays": 3650 }`); it only stops future sweeps.

**Linear** shows your assigned issues by default; the tree (`ctrl+p`) adds
Inbox, All Issues and pinned projects. Lists load a page at a time
(`linear.filter.limit`, 100) and the next as you reach the end; Linear
gives no total, so the status line reads `100 loaded · more below` until the
last page is in, and `fetching more…` with the tab spinner while a page is on its way. Rows carry priority, state,
`id · project · assignee`, comment count and labels. Sorts: date, status
(in progress, todo, triage, backlog), project, priority.

## Cross-references

Views link to each other and `l` follows the link: a PR to the Linear issue
in its title, branch or body; an issue to its attached PRs (with live
status glyphs) and the sessions that mention it; a session to the issues
and PRs in its transcript. The picker always confirms, even for one target.
Targets loaded in another view jump there; the rest open in the browser,
marked `↗`.

A small shared store (`internal/store`) is how views enrich each other
without importing one another: each publishes the facts it owns and reads
the others'. A view exposes links by implementing `ui.Referencer` and
becomes a destination with `ui.RefTarget`.

## Releasing

Merges to `main` accumulate into a draft release; publishing it tags
`vX.Y.Z` and GoReleaser builds the binaries. Labels drive the version and
the notes: the PR title's conventional prefix sets the type label (`feat:`
bumps minor, `breaking` major, the rest patch), changed paths add area
labels for triage. If the notes look wrong, check the PR's labels.

## Credit

The PRs view and much of the design follow [gh-dash](https://github.com/dlvhdr/gh-dash)
by Dolev Hadar (MIT): the tabbed views, the two-line rows, fetching through
GraphQL for check and review state, the glyph vocabulary, Glamour previews.
The jobs pane follows its companion [gh-enhance](https://github.com/dlvhdr/gh-enhance).
Nothing is vendored; it was rebuilt from reading the source. If you live in
one repo and want the full original, use gh-dash. agenda's reason to exist
is PRs plus sessions plus Linear in one switcher.

Built with [Bubble Tea v2](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss) and
[Glamour](https://github.com/charmbracelet/glamour).

## License

MIT. gh-dash's [license](https://github.com/dlvhdr/gh-dash/blob/main/LICENSE.txt)
covers the ideas this builds on.
