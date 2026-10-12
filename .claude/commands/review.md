Review this pull request and report only what needs fixing. With no PR
context, review the branch against main: `git diff main...HEAD`.

## Read the contract first

1. `DESIGN.md`: the interaction rules (reserved keys, pane focus, floats,
   `esc`, selection across refreshes, cross-references). Breaking one is a
   regression even when the tests pass.
2. `AGENTS.md` and `CONTRIBUTING.md`: the conventions. The ones that bite:
   - a new config key needs a row in the `ctrl+s` settings overlay
   - a new key binding needs a `keyRegistry` entry
   - an opinionated UX change sits behind a config key whose default is the
     existing behaviour
   - a change to how the app is released, operated or configured updates
     README.md or CONTRIBUTING.md in the same PR
   - no em-dashes in prose: commit messages, comments, docs, PR text
   - comments say why, not what; at most two lines, one per function

Treat a violation as a finding.

## What to look for

- Bugs and logic errors; a message that reaches every view through the
  root model's broadcast and is handled by the wrong one (`from *View` and
  the `foreign` guard exist for this)
- A `tea.Batch` whose commands are assumed to land in order
- A preview change that bypasses the preview cache (`PreviewKey`,
  `syncPreviewKey`, memo keys that omit a new input)
- Tests that would pass against the old behaviour, or a behaviour change
  without a test
- Docs drift: DESIGN.md, README.md, `config.example.yml` out of step with
  the change

Skip: style gofmt and golangci-lint already enforce, compliments,
descriptions of what the code does, speculative refactors.

## How to report

- One inline comment per finding that should block the merge or needs a
  decision, on the line it is about: the problem in one or two sentences,
  then the fix. These threads gate the merge, so open one only for
  something you would hold a merge for.
- Lower-stakes suggestions go in the summary comment, not as threads.
- The summary comment is short: findings count, then each lesser
  suggestion as one line. Nothing to report means say so in one line.
- Be direct. No preamble, no tour of the diff.
