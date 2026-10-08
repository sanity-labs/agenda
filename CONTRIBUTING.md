# Contributing

Short version: read two files, run one command, open a draft.

## Read first

- [DESIGN.md](DESIGN.md): how the core interactions work (reserved keys,
  pane focus, floats, `esc`, rendering rules). These are deliberate;
  breaking one is a regression even when the tests pass.
- [AGENTS.md](AGENTS.md): the conventions, written for coding agents but
  the same for people. Config keys need a settings row, key bindings need a
  registry entry, comments say why.
- [Project layout](#project-layout) below: where things live and the one
  rule that keeps views decoupled.

## Project layout

| Package | What lives there |
| :-- | :-- |
| `main.go`, `cli.go`, `update_cmd.go` | Entry point, the CLI commands (`help`, `version`, `update`, `completion`), config loading, wiring the views |
| `internal/tui` | Root model: tabs, layout, key and mouse routing, floats, the settings overlay, cross-reference picker |
| `internal/ui` | Shared widgets: list, two-line rows, label pills, scrollbar, picker, glyphs, markdown rendering, ref builders |
| `internal/views/prs` | GitHub pull requests: searches, sections, diff and file list, comments, CI jobs, review popup |
| `internal/views/linear` | Linear issues: sources (assigned, all, inbox, projects), nav tree, comments, paging |
| `internal/views/sessions` | Local agent sessions: transcript scan and cache, cost estimates, resume |
| `internal/config` | XDG config loading, keymap, settings persistence back to the file |
| `internal/store` | Shared metadata the views publish and read (PR status, issue titles, session mentions) |
| `internal/cache` | On-disk JSON cache, so the app paints before the first fetch lands |
| `internal/notify` | Desktop and in-app notifications |
| `internal/update` | The release check behind `agenda update` and the tab-bar marker |

`tui` never imports a view; `main` wires them in. A view is anything
implementing `tui.View`; it gains cross-references by also implementing
`ui.Referencer` / `ui.RefTarget`, and shares data with the other views
through the store rather than by importing them.

## Working on it

```sh
make help      # the targets
make run       # from source (go run ., not main.go: the commands live in cli.go)
make check     # what CI runs: formatting, vet, tests with the race detector
make lint      # golangci-lint
```

Tests: `go test ./...` is the whole suite and takes a few seconds. Two
habits that have caught real bugs here: check a new test fails against the
old behaviour before trusting it, and render layout changes and look at
them rather than reasoning about the arithmetic.

## Pull requests

- Open as a **draft** and mark it ready once it has been run locally
  against live accounts. Most of what has gone wrong here was invisible to
  the unit tests and obvious in the terminal.
- The **PR title** is a conventional commit (`feat(prs): …`, `fix(tui): …`,
  `docs:`, `ci:`, `chore:`). It sets the release label: `feat` bumps the
  minor version, `breaking` the major, the rest the patch, and the notes
  are grouped by it. See [README.md › Releasing](README.md#releasing).
- Keep opinionated UX changes behind a config key whose default is the
  existing behaviour, with a row in the `ctrl+s` overlay.
- Say what the problem was, what caused it, and what the change does.
  Skip the tour.

## Releasing

Merges to `main` accumulate into a draft release; publishing it tags
`vX.Y.Z` and GoReleaser builds the binaries. Nothing ships until someone
publishes the draft.
