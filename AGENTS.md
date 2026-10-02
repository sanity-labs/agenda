# Working on agenda

A TUI dashboard unifying GitHub PRs, local agent sessions, and Linear
issues.

## Read first

**[DESIGN.md](DESIGN.md)** describes how the core interactions work:
reserved keys, pane focus, what `esc` does, and the rendering rules.
These are deliberate. Breaking one is a regression even when the code
compiles and the tests pass, so read it before changing key handling,
focus, or anything that lays out a pane.

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
