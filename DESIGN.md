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
| `←` `→` | moving focus between the list and the pane; closing a float |
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

- **Beside a visible list the levels are** `list → pane open → pane
  focused`. `→` walks up: it focuses an open pane. `←` walks down: from a
  focused pane it hands the keys back with the pane still open; with the
  keys on the list and a pane open, it takes the pane back to the
  description. The configured pane is the floor and never closes, since it
  is the state you asked for.
- **Jump keys.** `pgup`/`pgdn` and `shift+↑`/`shift+↓` (`list.jump_up`,
  `list.jump_down`, rebindable) move whatever has the keys by `list_jump`
  (5 by default): rows of the list, lines of a focused scrolling pane,
  rows of the jobs pane or tree, and lines of the file list, where they
  only scroll. Like the arrows, they never reach the list from inside a
  pane, so they cannot move the selection and close a float from under you.
- **The file list's arrows are contextual.** `↑`/`↓` move to the
  neighbouring file when its row is on screen; otherwise they scroll a
  line, so a diff taller than the pane can be read without the cursor
  leaving it. The root model tells the view what the pane shows
  (`SetPreviewViewport`) so it can decide; `space` still advances and
  brings the next file into view. The wheel over a float scrolls the
  float, never the list behind it. There is no separate "scroll the preview
  from the list" key by default; `global.preview_*` stay bindable.
- A **float takes the keys on open**, whatever opened it and whatever it
  holds, including a floated description. The list is behind it, so there
  is nothing else the arrows could belong to.
- **A float has levels, and `←` steps one.** `v` opens level one, the
  description; a pane toggled over it (`c`, `d`, `t`) is level two. An
  arrow that would hand the keys back beside a list (`←` from a scrolling
  pane, `←` with nothing left to collapse in the file list or jobs pane,
  `←` on the description) steps down a level: back to the `v`
  description if there is one, otherwise the float closes. `→` has
  nothing deeper to step into, so in a float it does nothing beyond what
  the file list and jobs pane make of it (expand, open). A pane opened
  straight from the list is level one itself, so `←` closes it rather
  than dropping onto a description nobody asked for. Toggling a pane off
  with its own key is the same step. `floatBase` records that level one
  was opened; it is set when a float settles on the description.
- A pane opened **beside a visible list** waits to be asked, since both
  are visible and either could reasonably take the arrows.
- Changing panes drops focus, so a pane that has just appeared never
  silently owns the keys.
- **Focus cannot outlive its pane.** `PaneFocused` is tied to a pane being
  open, not just to the flag, so a pane closed by any route (a submitted
  review, a toggle reset) cannot leave the list dimmed with nothing
  focused. The flags are cleared too, so a reopened pane starts fresh.
- **A float arrives as `PreviewShownMsg(true)`, same as a side pane.**
  "Shown" means the detail is on screen; it says nothing about *where*.
  Only `PreviewFloatingMsg` distinguishes a float from a side pane, so
  focus keys off that flag and never off `previewShown` alone. Reading
  "hidden" as "floating" gets it wrong twice: the list dims at startup
  with nothing open, and a real float fails to take the keys because the
  reveal that opens it reports shown.
- **The two preview messages arrive in either order.** `setPreview`
  batches them and `tea.Batch` runs each command in its own goroutine, so
  neither handler may assume the other has run. Both call `floatFocus`,
  which settles focus from the combined state; a handler that decides from
  its own message alone loses focus on `v` whenever "shown" lands first.
  The key handler also calls `FocusPane` immediately while those messages
  land after, so that call cannot be relied on to survive.

Panes that hold their own cursor (the jobs pane, the file list) keep it.
Panes that scroll (diffs, comments) get the arrows and page keys routed
to the preview instead.

In Linear the nav tree already owns `←`, so its preview takes `→`. The
gesture is symmetric there rather than borrowed. Floats follow the same
levels as the PRs view (`v` is the description, `c` over it is level two,
`←` steps down, `esc` closes). Beside a visible list three places can hold
the keys, tree · list · pane, and `←` walks them right to left: a focused
pane hands the keys to the list, and only the list hands them to the tree.
`ctrl+p` is "take me to the tree": it shows the tree and puts the keys in
it, or just focuses it when it is already up (a permanently-on tree
included); pressed with the tree focused, it hides it. Taking the keys
there closes whatever pane or float is open, so the tree never becomes a
fourth thing to arrow between. Stepping an open comments
pane back to the description beside a list is `esc`. The two previews are headed alike (Description, Comments),
and the comments hint is clickable in both.

## esc

`esc` steps back one layer and **never closes agenda**. `q` closes the
same layers one at a time and quits only from the bare list, the way a
pager's `q` closes the pager; `ctrl+c` quits from anywhere. The focused
view gets the key first and reports whether it handled it, so a pane
unwinds its own state before the root model closes anything. `←` and `esc` both step beside a list; they part ways in a
float, where `←` steps a level and `esc` closes the window.

With the preview pane on:

```
job log → jobs/file-list focus → pane focus → the pane → zoom → filter
```

The filter is the floor: with nothing open over the list, `esc` clears the
query that narrowed it (from `/` or `f` alike), so the list is whole again.

Floated, `esc` does not step: it closes the whole float from any level.
The arrows are what walk back a level at a time (see Focus), so `esc`
is the one key that is always "get me out of this window".

A preview pane you configured on is never closed by `esc`: it undoes what
was opened over the list, not the configured state.

## Settings that need a reload

A row marked `reload required` changed in the `ctrl+s` overlay is
*pending* until applied. The panel shows a boxed notice and offers `r`,
which quits and execs the binary again: a real restart, so the option
takes effect exactly as it would after quitting by hand, with no second
code path to keep in step. Leaving the panel with something pending
(`esc`, or a click outside) collapses it to a single warning box: `r`
reloads, `esc` reverts the pending rows in the live config and the file
and closes, any other key returns to the panel. The file never says one
thing while the running app does another. Changing a row back to its
original value clears its pending state.

## Selection across refreshes

The list keeps the selection on the same *item*, not the same row: rows
carry a stable identity (`Key`: a PR's URL, an issue's identifier, a
session's transcript path) that `SetItems` follows, so a re-sort, a rename
or a status change does not move the cursor onto something else. When the
selected item leaves the list (approved and hidden, merged, filtered out),
the cursor stays at its index, on the row that slid into its place, rather
than jumping to the top and leaving you somewhere else entirely.

## Cross-references

`l` from a list raises the picker of what the selection links to, and
`enter` there jumps in-app when a view can take it: loaded already, or
fetched in by a view that can (`FetchRef`), so a Linear issue referenced
from a PR lands in the Linear tab even when its source does not list it.
The browser is the fallback, marked `↗` in the picker. Inside a focused
detail pane `l` does not raise a picker over the pane; it expands the
pane's own related section (an issue's pull requests), which is also
clickable. A PR's detail names its Linear issue with the jump hint.

## Panes

Each pane is one way of looking at the selected row, not a mode. They are
mutually exclusive, and the key that opened one closes it.

| key | pane |
|---|---|
| `d` | the diff: a file list with `github.diff_pane`, otherwise `less` |
| `c` | comments |
| `t` | CI jobs |

The review popup (`r`) opens the **file list** beside it by default, where
`space` marks files reviewed as you go; `github.review_view: unified`
opens the flat diff instead. A nudge in the popup for files not yet
marked is still to come.

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
