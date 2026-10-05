package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PickerItem is one row in a Picker. A normal item has a Label and an optional
// dimmed Detail line beneath it. A Separator item is a non-selectable group
// divider (its Label, if any, is shown in the rule).
type PickerItem struct {
	Label     string
	Detail    string
	Separator bool
}

// Picker is a small modal list for choosing one option (e.g. which referenced
// item to jump to). The host model owns it, routes key messages to Update while
// it's open, and composites View over its content.
type Picker struct {
	title  string
	items  []PickerItem
	cursor int

	// Filter mode (NewFilterPicker), for lists too long to show whole:
	// typing narrows the items to those matching query, and a window of at
	// most rows items scrolls over them. cursor then indexes shown.
	filtering bool
	query     string
	shown     []int // indexes into items, in display order
	rows      int
	offset    int // first shown index in the window
}

func NewPicker(title string, items []PickerItem) Picker {
	p := Picker{title: title, items: items}
	if len(items) > 0 && items[0].Separator {
		p.cursor = p.nextSelectable(0, 1) // never rest on a separator
	}
	return p
}

// NewFilterPicker is a picker for a long list: typing filters it, and at most
// rows items show at a time. Separators are not supported.
func NewFilterPicker(title string, items []PickerItem, rows int) Picker {
	p := Picker{title: title, items: items, filtering: true, rows: max(rows, 1)}
	p.refilter()
	return p
}

// refilter recomputes the matching items: labels containing the query come
// first, then those merely containing its letters in order, each group in
// the items' original order.
func (p *Picker) refilter() {
	q := strings.ToLower(p.query)
	var exact, loose []int
	for i, it := range p.items {
		l := strings.ToLower(it.Label)
		switch {
		case strings.Contains(l, q):
			exact = append(exact, i)
		case matchesSubsequence(l, q):
			loose = append(loose, i)
		}
	}
	p.shown = append(exact, loose...)
	p.cursor, p.offset = 0, 0
}

// moveFiltered steps the cursor through the matches, keeping it in the window.
func (p *Picker) moveFiltered(d int) {
	if len(p.shown) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+d, 0), len(p.shown)-1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+p.rows {
		p.offset = p.cursor - p.rows + 1
	}
}

// updateFiltered handles keys in filter mode, where printable keys type into
// the query instead of acting as shortcuts.
func (p *Picker) updateFiltered(km tea.KeyMsg) PickerAction {
	switch km.String() {
	case "up", "ctrl+p":
		p.moveFiltered(-1)
	case "down", "ctrl+n":
		p.moveFiltered(1)
	case "pgup":
		p.moveFiltered(-p.rows)
	case "pgdown":
		p.moveFiltered(p.rows)
	case "enter":
		if len(p.shown) > 0 {
			return PickerConfirm
		}
	case "esc", "ctrl+c":
		return PickerCancel
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.refilter()
		}
	case "ctrl+u":
		p.query = ""
		p.refilter()
	default:
		if kp, ok := km.(tea.KeyPressMsg); ok && kp.Text != "" {
			p.query += kp.Text
			p.refilter()
		}
	}
	return PickerNone
}

// nextSelectable returns the next non-separator index from start in direction
// dir (+1/-1), or the original cursor if there is none.
func (p *Picker) nextSelectable(start, dir int) int {
	for i := start; i >= 0 && i < len(p.items); i += dir {
		if !p.items[i].Separator {
			return i
		}
	}
	return p.cursor
}

// PickerAction is what the host model should do after a key is handled.
type PickerAction int

const (
	PickerNone    PickerAction = iota // key consumed, nothing to do
	PickerConfirm                     // follow the selected ref (Index)
	PickerOpenURL                     // open the selected ref in the browser
	PickerCancel                      // dismiss the modal
)

// Update handles navigation and returns the action the host should take. Read
// the selection with Index.
func (p *Picker) Update(msg tea.Msg) PickerAction {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return PickerNone
	}
	if p.filtering {
		return p.updateFiltered(km)
	}
	switch km.String() {
	case "up", "k":
		p.cursor = p.nextSelectable(p.cursor-1, -1)
	case "down", "j":
		p.cursor = p.nextSelectable(p.cursor+1, 1)
	case "enter":
		return PickerConfirm
	case "o":
		return PickerOpenURL
	case "esc", "ctrl+c", "q":
		return PickerCancel
	}
	return PickerNone
}

// Index is the selected option's index into the items, or -1 when a filter
// matches nothing.
func (p *Picker) Index() int {
	if !p.filtering {
		return p.cursor
	}
	if len(p.shown) == 0 {
		return -1
	}
	return p.shown[p.cursor]
}

// View renders the modal box. The caller composites it over its own content.
func (p *Picker) View() string {
	if p.filtering {
		return p.viewFiltered()
	}
	dim := Faint
	accent := Accent
	bold := lipgloss.NewStyle().Bold(true)

	var b strings.Builder
	b.WriteString(bold.Render(p.title))
	b.WriteString("\n\n")
	for i, it := range p.items {
		if it.Separator {
			rule := "────────"
			if it.Label != "" {
				rule = "── " + it.Label + " ──────"
			}
			b.WriteString("  ")
			b.WriteString(dim.Render(rule))
			b.WriteByte('\n')
			continue
		}
		if i == p.cursor {
			b.WriteString(accent.Render("▌ "))
			b.WriteString(bold.Render(it.Label))
		} else {
			b.WriteString("  ")
			b.WriteString(it.Label)
		}
		b.WriteByte('\n')
		if it.Detail != "" {
			b.WriteString("    ")
			b.WriteString(dim.Render(Truncate(it.Detail, 70)))
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	b.WriteString(dim.Render("↑/↓ select · enter open · o browser · esc cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Pal().Accent)).
		Padding(1, 2).
		Render(b.String())
}

// viewFiltered renders filter mode: the query with a match count, then the
// window of matches. The box keeps a fixed size so it doesn't jump around as
// the matches change.
func (p *Picker) viewFiltered() string {
	const innerW = 56
	bold := lipgloss.NewStyle().Bold(true)

	var b strings.Builder
	b.WriteString(bold.Render(p.title))
	b.WriteString("\n\n")
	query := Accent.Render("› ") + p.query + "█"
	count := Faint.Render(fmt.Sprintf("%d/%d", len(p.shown), len(p.items)))
	b.WriteString(query + strings.Repeat(" ", max(1, innerW-lipgloss.Width(query)-lipgloss.Width(count))) + count)
	b.WriteString("\n\n")
	for row := range p.rows {
		i := p.offset + row
		switch {
		case i < len(p.shown) && i == p.cursor:
			b.WriteString(Accent.Render("▌ ") + bold.Render(Truncate(p.items[p.shown[i]].Label, innerW-2)))
		case i < len(p.shown):
			b.WriteString("  " + Truncate(p.items[p.shown[i]].Label, innerW-2))
		case row == 0:
			b.WriteString(Faint.Render("  No matches."))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(Faint.Render("type to filter · ↑/↓ select · enter choose · esc cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Pal().Accent)).
		Padding(1, 2).
		Width(innerW + 6).
		Render(b.String())
}
