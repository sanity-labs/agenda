package prs

import (
	"regexp"
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
	v.applySort() // the band shows the editor, so it has to be rebuilt
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
		path, prev := f.path, v.cfg.Filter
		if path != "github.filter" {
			prev = v.cfg.ReviewFilter
		}
		v.filterEd = nil
		// Try it before keeping it. GitHub answers a query naming an author
		// it cannot resolve with a clean zero and no error, so a typo looks
		// exactly like "nothing matches" — and persisting that would leave
		// an empty list with the editor the only way back.
		if path == "github.filter" {
			v.cfg.Filter = q
		} else {
			v.cfg.ReviewFilter = q
		}
		v.loading = true
		v.applySort()
		return tryFilter(path, q, prev)
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
	// The band carries the text being typed, so every key rebuilds it.
	v.applySort()
	return nil
}

// filterPromptLine renders the open editor for the list header.
func (v *View) filterPromptLine() string {
	f := v.filterEd
	return ui.Yellow.Render(ui.Glyph(ui.IconSearch, "?")+" "+f.label+": ") +
		f.query + "█" +
		ui.Faint.Render("  (enter apply · ctrl+u clear · esc cancel)")
}

// filterTriedMsg reports whether an edited filter returned anything, and
// what the same search returns without its author terms: a query that is
// empty only because of an author is a name GitHub could not resolve.
type filterTriedMsg struct {
	path, query, prev string
	got, without      int
	err               error
}

// authorTermRe finds author qualifiers, the ones that silently void a
// query when the name does not resolve.
var authorTermRe = regexp.MustCompile(`(?i)-?author:\S+`)

// tryFilter runs an edited query, and when it comes back empty runs it
// again without its author terms to tell "nothing matches" apart from "no
// such author". Only the first page, since the count is all this needs.
func tryFilter(path, q, prev string) tea.Cmd {
	return func() tea.Msg {
		page, err, _ := searchPRs(ensurePR(q), 1, "")
		msg := filterTriedMsg{path: path, query: q, prev: prev,
			got: page.total, err: err}
		if err != nil || page.total > 0 || !authorTermRe.MatchString(q) {
			return msg
		}
		bare := strings.TrimSpace(authorTermRe.ReplaceAllString(q, ""))
		if bare == "" {
			return msg
		}
		if alt, err, _ := searchPRs(ensurePR(bare), 1, ""); err == nil {
			msg.without = alt.total
		}
		return msg
	}
}

// badAuthor reports a filter that matched nothing only because of an
// author term: the same search without it finds rows.
func (m filterTriedMsg) badAuthor() bool {
	return m.err == nil && m.got == 0 && m.without > 0
}
