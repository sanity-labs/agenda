package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The navigation pane: a Linear-desktop-style tree on the far left with the
// basic sources (My Issues, All Issues) and your pinned projects under a
// favourites section. ctrl+p toggles it (linear.nav sets the startup
// default), left/right move focus between the tree and the list, enter
// applies the selected source and refetches.

// navSource describes where the list's issues come from. Comparable, so a
// source switch is detectable (notifications stay quiet across switches).
type navSource struct {
	Kind      string // "assigned" | "all" | "project"
	ProjectID string
	Label     string
}

func sourceForScope(scope string) navSource {
	if scope == "all" {
		return navSource{Kind: "all", Label: "All Issues"}
	}
	return navSource{Kind: "assigned", Label: "My Issues"}
}

// navItem is one row of the tree; header rows are not selectable.
type navItem struct {
	header bool
	source navSource
}

// navProject is a pinned project from the favourites query.
type navProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type favsMsg struct {
	projects []navProject
	err      error
}

const favoritesQuery = `query {
  favorites(first: 50) {
    nodes { type project { id name } }
  }
}`

// fetchFavs loads the user's pinned projects for the favourites section.
func (v *View) fetchFavs() tea.Cmd {
	token := v.token
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]any{"query": favoritesQuery})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return favsMsg{err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return favsMsg{err: err}
		}
		defer resp.Body.Close()

		var out struct {
			Data struct {
				Favorites struct {
					Nodes []struct {
						Type    string      `json:"type"`
						Project *navProject `json:"project"`
					} `json:"nodes"`
				} `json:"favorites"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return favsMsg{err: err}
		}
		if len(out.Errors) > 0 {
			return favsMsg{err: fmt.Errorf("linear: %s", out.Errors[0].Message)}
		}
		var projects []navProject
		for _, n := range out.Data.Favorites.Nodes {
			if n.Type == "project" && n.Project != nil {
				projects = append(projects, *n.Project)
			}
		}
		return favsMsg{projects: projects}
	}
}

// rebuildNav lays out the tree from the fixed sources and loaded favourites.
func (v *View) rebuildNav() {
	items := []navItem{
		{source: navSource{Kind: "inbox", Label: "Inbox"}},
		{source: navSource{Kind: "assigned", Label: "My Issues"}},
		{source: navSource{Kind: "all", Label: "All Issues"}},
	}
	if len(v.favs) > 0 {
		items = append(items, navItem{header: true, source: navSource{Label: "Favourites"}})
		for _, p := range v.favs {
			items = append(items, navItem{source: navSource{Kind: "project", ProjectID: p.ID, Label: p.Name}})
		}
	}
	v.navItems = items
	if v.navSel >= len(items) {
		v.navSel = 0
	}
}

// navIcon is the decorative glyph for a tree entry, per source kind.
func navIcon(kind string) string {
	switch kind {
	case "inbox":
		return ui.Glyph(ui.IconNavInbox, "")
	case "assigned":
		return ui.Glyph(ui.IconNavMine, "")
	case "all":
		return ui.Glyph(ui.IconNavAll, "")
	case "project":
		return ui.Glyph(ui.IconNavProject, "")
	}
	return ""
}

// navMove steps the tree cursor, skipping headers.
func (v *View) navMove(d int) {
	if len(v.navItems) == 0 {
		return
	}
	i := v.navSel
	for {
		i += d
		if i < 0 || i >= len(v.navItems) {
			return // stay put at the edges
		}
		if !v.navItems[i].header {
			v.navSel = i
			return
		}
	}
}

// updateNav handles keys while the tree has focus. Returns the refetch
// command when a source is applied.
func (v *View) updateNav(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		v.navMove(-1)
	case "down", "j":
		v.navMove(1)
	case "right", "esc":
		v.navFocus = false
	case "enter":
		return v.applyNav()
	}
	return nil
}

// applyNav switches the list to the tree's selected source and hands focus
// back to the list. Returns the refetch command, or nil when the source is
// already showing.
func (v *View) applyNav() tea.Cmd {
	it := v.navItems[v.navSel]
	v.navFocus = false
	if it.header || it.source == v.source {
		return nil
	}
	v.source = it.source
	v.loading = true
	return v.fetch()
}

// clickNav applies the tree entry drawn at line y of the tree column, as
// selecting it and pressing enter would.
func (v *View) clickNav(y int) tea.Cmd {
	i := v.navItemAt(y)
	if i < 0 {
		return nil
	}
	v.navSel = i
	return v.applyNav()
}

// navItemAt maps a line of the rendered tree to the index of the entry drawn
// there, or -1 for the alignment line, a header, the error footer or empty
// space. It walks the same layout navView draws: one alignment line, then a
// line per entry, with each header taking two (a gap and its label).
func (v *View) navItemAt(y int) int {
	line := 1
	for i, it := range v.navItems {
		if it.header {
			line += 2
			continue
		}
		if y == line {
			return i
		}
		line++
	}
	return -1
}

// navWidth is the tree column's content width (0 when hidden).
func (v *View) navWidth() int {
	if !v.navShown {
		return 0
	}
	return min(24, max(14, v.listW/4))
}

// navView renders the tree column, padded to the pane height, with a dim
// right border separating it from the list.
func (v *View) navView() string {
	w := v.navWidth()
	var lines []string
	lines = append(lines, "") // aligns with the list's header line
	for i, it := range v.navItems {
		if it.header {
			// The favourites header: a star, the label, and a dim rule.
			head := ui.Yellow.Render(ui.Glyph(ui.IconStar, "")) +
				ui.Yellow.Bold(true).Render(it.source.Label)
			fill := max(0, w-lipgloss.Width(head)-1)
			lines = append(lines, "", head+" "+ui.Dim.Render(strings.Repeat("─", fill)))
			continue
		}
		marker := "  "
		if it.source == v.source {
			marker = ui.Accent.Render("▌") + " "
		}
		icon := navIcon(it.source.Kind)
		// The marker column is exactly 2 cells; the icon is measured, not
		// assumed, since nerd-font glyph widths vary by environment.
		label := ui.Truncate(it.source.Label, max(1, w-2-lipgloss.Width(icon)))
		switch {
		case v.navFocus && i == v.navSel:
			label = ui.Accent.Bold(true).Render("› ") + ui.Accent.Render(icon+label)
		case it.source == v.source:
			label = ui.Text.Render(icon + label)
		default:
			label = ui.Dim.Render(icon + label)
		}
		lines = append(lines, marker+label)
	}
	if v.navErr != nil {
		lines = append(lines, "", ui.Faint.Render(ui.Truncate("favs: "+v.navErr.Error(), w)))
	}

	body := ""
	for i, l := range lines {
		if i > 0 {
			body += "\n"
		}
		body += l
	}
	// Width counts the border, so w+1 leaves the full w cells for content;
	// with just w the full-width favourites rule wrapped onto a line of its
	// own and pushed every project below it down a row.
	return lipgloss.NewStyle().
		Width(w + 1).
		Height(max(1, v.height)).
		BorderStyle(lipgloss.NormalBorder()).
		BorderRight(true).
		BorderLeft(false).BorderTop(false).BorderBottom(false).
		BorderForeground(lipgloss.Color(ui.Pal().Border)).
		Render(body)
}
