# Working on agenda

A TUI dashboard unifying GitHub PRs, local agent sessions, and Linear
issues.

## Read first

**[DESIGN.md](DESIGN.md)** describes how the core interactions work:
reserved keys, pane focus, what `esc` does, and the rendering rules.
These are deliberate. Breaking one is a regression even when the code
compiles and the tests pass, so read it before changing key handling,
focus, or anything that lays out a pane.

**[CONTRIBUTING.md](CONTRIBUTING.md)** has the project layout (where
things live, and the rule that keeps the views decoupled: `tui` never
imports a view, `main` wires them in, views share data through the store)
and the PR habits (draft until run locally, conventional-commit titles
since they drive the release labels, `make check` before pushing).

## Re-read the docs, every session

Read every `.md` in the repo root at the start of a session, not from
memory of a previous one: `DESIGN.md`, this file, `README.md`, and
`FEATURES.md` when it is present (it is kept local and uncommitted; it is
the live backlog). They change between sessions, and a summary carried
over from an earlier one is how a rule gets broken in good faith.

While reading, check each claim against the code it describes. Where the
two have drifted, or a documented decision no longer fits where the code
has gone, say so to the user before building on either: name the passage,
what the code does now, and whether the doc or the code looks like the
one to change. Do not silently update the doc to match the code, and do
not silently code around the doc. A documented choice is a decision
someone made; it may need reconsidering, but that is theirs to do.

## Conventions

- **Opinionated changes ship behind a config key** whose default is the
  existing behaviour. Agenda is a fork of obliadp's work; an upgrade
  should not surprise someone who liked it as it was.
- **Every config key needs a row in the `ctrl+s` overlay.** A test
  enforces it. A key you can only set by editing the file is one most
  people will never find.
- **Every `bind()` call needs an entry in `keyRegistry`.** A test reads
  the view sources to enforce it. Without one the key works but cannot be
  remapped and never appears in the keybind editor.
- **Comments say why, not what.** Default to none; write one where a
  reader who knows Go would still be surprised.

## Testing

`go test ./...` is the whole suite and runs in a few seconds.

Two habits worth keeping, both of which have caught real bugs here:

- **Check a new test fails against the old behaviour.** A test that
  passes either way proves nothing, and several here passed vacuously
  until deliberately broken.
- **Render it and look.** Layout bugs (a wrapped line, a drifting column,
  a band that stops mid-row) are obvious in output and invisible in the
  arithmetic that produced them.
