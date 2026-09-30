package ui

import "testing"

func TestParseQuery(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     []Term
	}{
		{"empty", "", nil},
		{"bare word", "persona", []Term{{Value: "persona"}}},
		{"qualifier", "label:deps", []Term{{Key: "label", Value: "deps"}}},
		{"negated qualifier", "-label:deps",
			[]Term{{Key: "label", Value: "deps", Negated: true}}},
		{"negated bare word", "-renovate",
			[]Term{{Value: "renovate", Negated: true}}},
		{"several terms", "-is:draft review:required persona", []Term{
			{Key: "is", Value: "draft", Negated: true},
			{Key: "review", Value: "required"},
			{Value: "persona"},
		}},
		{"quoted value keeps its space", `label:"needs review"`,
			[]Term{{Key: "label", Value: "needs review"}}},
		{"key is folded, value is not", "LABEL:Deps",
			[]Term{{Key: "label", Value: "Deps"}}},
		// A lone colon on either side is a typo, not a term: treating it as
		// a qualifier would silently match nothing.
		{"trailing colon is a word", "label:", []Term{{Value: "label:"}}},
		{"leading colon is a word", ":deps", []Term{{Value: ":deps"}}},
		{"a bare dash is a word", "-", []Term{{Value: "-"}}},
		{"extra spaces collapse", "  a   b  ",
			[]Term{{Value: "a"}, {Value: "b"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ParseQuery(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("ParseQuery(%q) = %+v, want %+v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("term %d = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestHasQualifier(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"persona", false},
		{"-renovate", false},
		{"label:deps", true},
		{"persona label:deps", true},
		{"label:", false},
	} {
		if got := HasQualifier(c.in); got != c.want {
			t.Errorf("HasQualifier(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
