# Design guidelines

How agenda's core interactions work, and why. These are deliberate, and a
change that breaks one of them is a regression even when it compiles and
the tests pass. Tests cover most of what follows; where a rule has no
test, that is a gap worth filling rather than permission to ignore it.

## Reserved keys

These always mean what they mean. They are how you get out of any state,
and a keymap that pointed one somewhere else would be unrecoverable
without editing the config file by hand.

**The key is fixed, not the action.** Binding more keys to the same
actions is expected: `hjkl` alongside the arrows, `pgup`/`pgdown` for
paging, `ctrl+p` for up. A reserved key is also still allowed on the
action it already means, so writing out a default (`list.up: [up, k]`)
keeps working. Only pointing a reserved key at something *else* is
refused.

| key | kept for |
|---|---|
| `←` `→` | moving focus between the list and the pane |
| `↑` `↓` | moving within whatever has focus |
| `esc` | stepping back out of whatever is open |
| `ctrl+c` | quitting |

`config.Of` drops a reserved key only from a binding that points it
somewhere else, and the rest of that binding still applies, so a
partly-reserved override is not thrown away whole. The startup message log says which were ignored, since
the key would otherwise keep its built-in meaning and the override would
look like it had simply not worked.

Every other key is rebindable, including the named actions (`d`, `c`,
`t`, `r`, …). A view's `bind()` call must have a matching entry in
`keyRegistry`, or the key works but cannot be remapped and never appears
in the keybind editor — a test reads the `bind()` calls out of the view
sources to enforce this.

## Focus

The preview pane can take the keys. While it has them the list **dims and
drops its selection bar**, so exactly one cursor is lit on screen and it
is the one the arrows will move.

- `→` focuses the pane, `←` hands the keys back.
- A **float always has the keys**, whatever opened it and whatever it
  holds — including a floated description. The list is behind it, so
  there is nothing else the arrows could belong to, and `←`/`→` do not
  move focus there: `esc` is the way out.
- A pane opened **beside a visible list** waits to be asked, since both
  are visible and either could reasonably take the arrows.
- Changing panes drops focus, so a pane that has just appeared never
  silently owns the keys.
- **Focus cannot outlive its pane.** `PaneFocused` is tied to a pane being
  open, not just to the flag, so a pane closed by any route (a submitted
  review, a toggle reset) cannot leave the list dimmed with nothing
  focused. The flags are cleared too, so a reopened pane starts fresh.

Panes that hold their own cursor (the jobs pane, the file list) keep it.
Panes that scroll (diffs, comments) get the arrows and page keys routed
to the preview instead.

In Linear the nav tree already owns `←`, so its preview takes `→`. The
gesture is symmetric there rather than borrowed.

## esc

`esc` steps back one layer and **never closes agenda** — that is `q`
alone. The focused view gets the key first and reports whether it handled
it, so a pane unwinds its own state before the root model closes
anything.

With the preview pane on:

```
job log → jobs/file-list focus → pane focus → the pane → zoom
```

Floated, there is no list beside the pane to hand focus back to, so the
steps collapse:

```
any pane → the description → the float closes
```

A preview pane you configured on is never closed by `esc`: it undoes what
was opened over the list, not the configured state.

## Panes

Each pane is one way of looking at the selected row, not a mode. They are
mutually exclusive, and the key that opened one closes it.

| key | pane |
|---|---|
| `d` | the diff: a file list with `github.diff_pane`, otherwise `less` |
| `c` | comments |
| `t` | CI jobs |

The review popup's "view diff" opens the **unified** diff rather than the
file list: reviewing reads better against a flat diff.

Both show the inline review threads, boxed under the line they are
anchored to. The file list derives each patch line's number in the new
file from the `@@` headers to match them; a removed line has no number,
since there is nothing on the new side for a thread to point at.

## Opinionated changes are configurable

Agenda started as obliadp's. Anything that changes how it behaves ships
behind a config key whose default is the original behaviour, so an
upgrade never surprises someone who liked it as it was.

Every config key must appear as a row in the `ctrl+s` overlay, or it can
only be set by editing the file and is effectively invisible. A test
enforces this; genuinely file-only keys (tokens, search queries, keymaps)
are listed explicitly as exceptions.

## Rendering

- **Columns hold still.** Cells are padded to fixed widths rather than
  sized to their contents, and large numbers switch unit (`+1.2k -567`)
  rather than widening. A column that moves with its contents gives the
  eye nothing to track down a list.
- **Nothing wraps.** A line longer than its box is truncated with an
  ellipsis. Wrapping grows the box, which pushes the footer off screen or
  silently eats a list row.
- **Measure, do not assume.** `lipgloss`'s `Width()` counts the border and
  padding inside the width it is given, Nerd Font glyphs are two columns
  wide, and `ui.Glyph` already appends a trailing space. Each of these has
  caused a layout bug here; a render test is faster than reasoning about
  them.
- **A style nested in a band ends with a reset**, which would close the
  band early and leave the rest of the row unfilled. `SectionSeparator`
  re-opens the band after any nested reset.
