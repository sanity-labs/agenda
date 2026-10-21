package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func searchKey(s *SearchBox, k string) SearchAction {
	if len(k) == 1 {
		return s.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
	}
	code := map[string]rune{"backspace": tea.KeyBackspace, "enter": tea.KeyEnter}[k]
	return s.Update(tea.KeyPressMsg{Code: code})
}

// An answer for a query the box has moved past is dropped, and backspace on
// an empty query drops the scope chip before it does nothing.
func TestSearchBoxStaleResultsAndScope(t *testing.T) {
	s := NewSearchBox("x")
	if searchKey(&s, "1") != SearchChanged || searchKey(&s, "2") != SearchChanged {
		t.Fatal("typing should ask for a new search")
	}
	if s.SetResults(SearchResultsMsg{Query: "1", Results: []Ref{{ID: "a"}}}, []string{"a"}) {
		t.Error("results for an older query were taken")
	}
	if !s.SetResults(SearchResultsMsg{Query: "12", Results: []Ref{{ID: "b"}}}, []string{"b"}) {
		t.Fatal("results for the current query were dropped")
	}
	if searchKey(&s, "enter") != SearchConfirm {
		t.Error("enter on a result should confirm")
	}
	s.Narrow("o/r")
	if s.Query != "" || s.Scope != "o/r" {
		t.Fatalf("narrow: query=%q scope=%q", s.Query, s.Scope)
	}
	if searchKey(&s, "backspace") != SearchChanged || s.Scope != "" {
		t.Errorf("backspace on an empty query kept scope %q", s.Scope)
	}
	if searchKey(&s, "backspace") != SearchNone {
		t.Error("backspace with nothing left should do nothing")
	}
}
