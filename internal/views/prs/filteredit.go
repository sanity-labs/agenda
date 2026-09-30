package prs

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// filterEdit is the open search-filter editor. Editing the search is a
// different job from the in-app filter: '/' narrows the rows already
// loaded, this changes what gets fetched at all, so it refetches and
// persists rather than matching locally.
type filterEdit struct {
	// path is the config key being edited, so the change is written where
	// it was read from.
	path  string
	label string
	query string
}

// editFilter opens the editor on whichever search produced the selected
// row: your own PRs, or the review-requested section. The cursor already
// says which you mean, so it needs no extra prompt.
func (v *View) editFilter() tea.Cmd {
	path, label, q := "github.filter", "my PRs", v.cfg.Filter
	if v.inReviewSection() {
		path, label, q = "github.review_filter", "review requests", v.cfg.ReviewFilter
	}
	v.filterEd = &filterEdit{path: path, label: label, query: q}
	return nil
}

// inReviewSection reports whether the cursor is in the review-requested
// half of the list.
func (v *View) inReviewSection() bool {
	sel := v.list.Selected()
	if sel.URL == "" {
		return false
	}
	for _, p := range v.reviewRaw {
		if p.URL == sel.URL {
			return true
		}
	}
	return false
}

// updateFilterEdit handles keys while the search-filter editor is open.
func (v *View) updateFilterEdit(msg tea.KeyMsg) tea.Cmd {
	f := v.filterEd
	switch msg.String() {
	case "esc":
		v.filterEd = nil
	case "enter":
		q := strings.TrimSpace(f.query)
		path := f.path
		v.filterEd = nil
		// Apply it to the live config so the refetch uses it, and tell the
		// root model to persist it: a filter you had to retype every run
		// would not be worth the keystroke.
		if path == "github.filter" {
			v.cfg.Filter = q
		} else {
			v.cfg.ReviewFilter = q
		}
		v.loading = true
		return tea.Batch(
			func() tea.Msg { return ui.ConfigSetMsg{Path: path, Value: q} },
			v.fetch(),
		)
	case "backspace":
		if f.query != "" {
			f.query = f.query[:len(f.query)-1]
		}
	case "ctrl+u":
		f.query = "" // clear the line, as a shell would
	default:
		if kp, ok := tea.Msg(msg).(tea.KeyPressMsg); ok && kp.Text != "" {
			if r := []rune(kp.Text)[0]; r >= 0x20 && r != 0x7f {
				f.query += kp.Text
			}
		}
	}
	return nil
}

// filterPromptLine renders the open editor for the list header.
func (v *View) filterPromptLine() string {
	f := v.filterEd
	return ui.Yellow.Render(ui.Glyph(ui.IconSearch, "?")+" "+f.label+": ") +
		f.query + "█" +
		ui.Faint.Render("  (enter apply · ctrl+u clear · esc cancel)")
}
