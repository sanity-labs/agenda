package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestPickerNavigation(t *testing.T) {
	p := NewPicker("Follow", []PickerItem{{Label: "a"}, {Label: "b"}, {Label: "c"}})
	if p.Index() != 0 {
		t.Fatalf("initial Index = %d, want 0", p.Index())
	}

	p.Update(press(tea.KeyDown))
	p.Update(press(tea.KeyDown))
	if p.Index() != 2 {
		t.Errorf("after two downs Index = %d, want 2", p.Index())
	}
	p.Update(press(tea.KeyDown)) // clamps at the bottom
	if p.Index() != 2 {
		t.Errorf("Index = %d, want clamped at 2", p.Index())
	}
	p.Update(press(tea.KeyUp))
	if p.Index() != 1 {
		t.Errorf("after up Index = %d, want 1", p.Index())
	}
}

func TestPickerVimKeys(t *testing.T) {
	p := NewPicker("x", []PickerItem{{Label: "a"}, {Label: "b"}})
	p.Update(press('j'))
	if p.Index() != 1 {
		t.Errorf("after j Index = %d, want 1", p.Index())
	}
	p.Update(press('k'))
	if p.Index() != 0 {
		t.Errorf("after k Index = %d, want 0", p.Index())
	}
}

func TestPickerSkipsSeparators(t *testing.T) {
	p := NewPicker("x", []PickerItem{
		{Label: "a"},
		{Separator: true, Label: "sessions"},
		{Label: "b"},
	})
	if p.Index() != 0 {
		t.Fatalf("initial Index = %d, want 0", p.Index())
	}
	p.Update(press(tea.KeyDown))
	if p.Index() != 2 {
		t.Errorf("down Index = %d, want 2 (separator skipped)", p.Index())
	}
	p.Update(press(tea.KeyUp))
	if p.Index() != 0 {
		t.Errorf("up Index = %d, want 0 (separator skipped)", p.Index())
	}
}

func TestPickerInitialCursorSkipsLeadingSeparator(t *testing.T) {
	p := NewPicker("x", []PickerItem{{Separator: true, Label: "s"}, {Label: "a"}})
	if p.Index() != 1 {
		t.Errorf("initial Index = %d, want 1 (leading separator skipped)", p.Index())
	}
}

func TestPickerActions(t *testing.T) {
	p := NewPicker("x", []PickerItem{{Label: "a"}, {Label: "b"}})
	if act := p.Update(press(tea.KeyEnter)); act != PickerConfirm {
		t.Errorf("enter -> %v, want PickerConfirm", act)
	}
	if act := p.Update(press('o')); act != PickerOpenURL {
		t.Errorf("o -> %v, want PickerOpenURL", act)
	}
	if act := p.Update(press(tea.KeyEscape)); act != PickerCancel {
		t.Errorf("esc -> %v, want PickerCancel", act)
	}
	if act := p.Update(press('x')); act != PickerNone {
		t.Errorf("unbound key -> %v, want PickerNone", act)
	}
}

func typeText(p *Picker, s string) {
	for _, r := range s {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestFilterPickerNarrowsAndRanksSubstringFirst(t *testing.T) {
	p := NewFilterPicker("Repo", []PickerItem{
		{Label: "argoproj/argo-rollouts"},
		{Label: "sanity-io/argocd-ops"},
		{Label: "sanity-io/ops"},
		{Label: "sanity-labs/agenda"},
	}, 5)

	labels := func() []string {
		var out []string
		for _, i := range p.shown {
			out = append(out, p.items[i].Label)
		}
		return out
	}

	// "o" and "s" are shortcuts in a plain picker; here they type. Labels
	// containing "ops" come first, then argo-rollouts, which only has the
	// letters in order.
	typeText(&p, "ops")
	want := []string{"sanity-io/argocd-ops", "sanity-io/ops", "argoproj/argo-rollouts"}
	if got := labels(); !slices.Equal(got, want) {
		t.Errorf("matches for ops = %q, want %q", got, want)
	}

	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.query != "op" {
		t.Errorf("after backspace query = %q, want op", p.query)
	}
	p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	typeText(&p, "agenda")
	if got := labels(); !slices.Equal(got, []string{"sanity-labs/agenda"}) {
		t.Errorf("matches for agenda = %q", got)
	}

}

func TestFilterPickerEnterNeedsAMatch(t *testing.T) {
	p := NewFilterPicker("Repo", []PickerItem{{Label: "sanity-io/ops"}}, 5)
	typeText(&p, "zzz")
	if p.Index() != -1 {
		t.Errorf("Index = %d with no matches, want -1", p.Index())
	}
	if a := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); a != PickerNone {
		t.Errorf("enter with no matches = %v, want PickerNone", a)
	}
	if a := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); a != PickerCancel {
		t.Errorf("esc = %v, want PickerCancel", a)
	}
}

func TestFilterPickerWindowFollowsCursor(t *testing.T) {
	var items []PickerItem
	for _, s := range []string{"a", "b", "c", "d", "e", "f"} {
		items = append(items, PickerItem{Label: s})
	}
	p := NewFilterPicker("Repo", items, 3)
	for range 4 {
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if p.items[p.Index()].Label != "e" || p.offset != 2 {
		t.Errorf("after 4 downs: selected %q offset %d, want e and 2", p.items[p.Index()].Label, p.offset)
	}
	view := p.View()
	if strings.Contains(view, "  a\n") || !strings.Contains(view, "e") {
		t.Errorf("window should show c..e, not a:\n%s", view)
	}
}
