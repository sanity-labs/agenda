package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
)

type barView struct {
	fatView
	bar string
}

func (b *barView) QueryBar(width int) string {
	if b.bar == "" {
		return ""
	}
	return lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).
		Width(width - 2).Render(b.bar)
}

// The bar sits above the footer, and the content gives up its rows so the
// footer is not pushed off the screen.
func TestQueryBarSitsAboveTheFooter(t *testing.T) {
	cfg := config.Default()
	v := &barView{fatView: fatView{title: "PRs"}, bar: "author:@me is:open"}
	m := New(cfg, []View{v})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	m.invalidateFrame()

	lines := strings.Split(m.View().Content, "\n")
	if len(lines) != 40 {
		t.Fatalf("rendered %d lines on a 40-row screen", len(lines))
	}
	// The footer is the last row and must survive.
	if last := lines[len(lines)-1]; !strings.Contains(last, "quit") {
		t.Errorf("last row is not the footer: %q", last)
	}
	// The bar is just above it: three rows of box ending at len-2.
	found := false
	for _, l := range lines[len(lines)-5:] {
		if strings.Contains(l, "author:@me") {
			found = true
		}
	}
	if !found {
		t.Error("the query bar is not in the last few rows")
	}
}

// With no bar, the content keeps those rows.
func TestNoBarGivesTheRowsBack(t *testing.T) {
	cfg := config.Default()
	withBar := &barView{fatView: fatView{title: "PRs"}, bar: "x"}
	m := New(cfg, []View{withBar})
	m.width, m.height, m.ready = 100, 40, true
	m.layout()
	_, _, withH := m.dims()

	without := &barView{fatView: fatView{title: "PRs"}}
	m2 := New(cfg, []View{without})
	m2.width, m2.height, m2.ready = 100, 40, true
	m2.layout()
	_, _, withoutH := m2.dims()

	if withoutH <= withH {
		t.Errorf("content height is %d without a bar and %d with one;"+
			" the rows were not given back", withoutH, withH)
	}
}
