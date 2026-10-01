package prs

import (
	"slices"
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

func TestPRFields(t *testing.T) {
	p := pr{
		Title:       "Add oauth",
		HeadRefName: "feat/oauth",
		Body:        "body text",
	}
	p.Repository.NameWithOwner = "sanity-io/agenda"
	p.Author.Login = "tiggi"

	want := map[string]string{
		"repo":        "sanity-io/agenda",
		"branch":      "feat/oauth",
		"title":       "Add oauth",
		"description": "body text",
		"author":      "tiggi",
	}
	got := map[string]string{}
	for _, f := range p.Fields() {
		got[f.Name] = f.Text
	}
	for name, text := range want {
		if got[name] != text {
			t.Errorf("Fields()[%q] = %q, want %q", name, got[name], text)
		}
	}
	// The qualifier-only fields are extra, so count the searchable ones.
	searchable := 0
	for _, f := range p.Fields() {
		if !f.Qualified {
			searchable++
		}
	}
	if searchable != len(want) {
		t.Errorf("Fields() has %d searchable fields, want %d", searchable, len(want))
	}
}

func TestLinearRefs(t *testing.T) {
	cases := []struct {
		name string
		pr   pr
		want []string
	}{
		{
			name: "uppercase in title parens",
			pr:   pr{Title: "docs: plan for shard migration (SRE-4419)"},
			want: []string{"SRE-4419"},
		},
		{
			name: "lowercase in title prefix",
			pr:   pr{Title: "ci(sre-4228): migrate to orb"},
			want: []string{"SRE-4228"},
		},
		{
			name: "from branch when title has none",
			pr:   pr{Title: "feat: add gateway routes", HeadRefName: "orjan/sre-3717-add-gateway"},
			want: []string{"SRE-3717"},
		},
		{
			name: "multiple refs in order, de-duplicated",
			pr:   pr{Title: "ENG-1 and ENG-2", Body: "also ENG-1 again and OPS-9"},
			want: []string{"ENG-1", "ENG-2", "OPS-9"},
		},
		{
			name: "title before branch before body",
			pr:   pr{Title: "do ENG-1", HeadRefName: "u/sre-2-x", Body: "ref OPS-3"},
			want: []string{"ENG-1", "SRE-2", "OPS-3"},
		},
		{
			name: "no reference",
			pr:   pr{Title: "fix the bug", HeadRefName: "fix/the-bug"},
			want: nil,
		},
		{
			name: "version token is not an issue ref",
			pr:   pr{Title: "bump to v2", HeadRefName: "chore/v2-bump"},
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pr.linearRefs(); !slices.Equal(got, c.want) {
				t.Errorf("linearRefs() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestReviewedByMe(t *testing.T) {
	cases := map[string]bool{
		"APPROVED":          true,
		"CHANGES_REQUESTED": true,
		"COMMENTED":         true,
		"DISMISSED":         false, // dismissed: a re-review is wanted
		"PENDING":           false, // unsubmitted draft review
		"":                  false,
	}
	for state, want := range cases {
		p := pr{}
		p.ViewerLatestReview.State = state
		if got := p.reviewedByMe(); got != want {
			t.Errorf("reviewedByMe(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestApplySortMarksOnlyReviewSection(t *testing.T) {
	mine := pr{Number: 1, Title: "mine", URL: "u1"}
	mine.ViewerLatestReview.State = "APPROVED" // self-review noise must not mark own PRs
	done := pr{Number: 2, Title: "done", URL: "u2"}
	done.ViewerLatestReview.State = "APPROVED"
	todo := pr{Number: 3, Title: "todo", URL: "u3"}

	v := New(config.GitHubConfig{MarkReviewed: true}, nil, nil, nil)
	v.showReview = true
	v.raw = []pr{mine}
	v.reviewRaw = []pr{done, todo}
	v.applySort()

	for _, p := range v.list.Items() {
		switch p.Number {
		case 1:
			if p.Reviewed {
				t.Error("own PR marked Reviewed; must apply to the review section only")
			}
		case 2:
			if !p.Reviewed {
				t.Error("approved review-requested PR not marked Reviewed")
			}
		case 3:
			if p.Reviewed {
				t.Error("unreviewed PR marked Reviewed")
			}
		}
	}
}

func TestApplySortMarkReviewedOffByDefault(t *testing.T) {
	done := pr{Number: 2, Title: "done", URL: "u2"}
	done.ViewerLatestReview.State = "APPROVED"

	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.showReview = true
	v.reviewRaw = []pr{done}
	v.applySort()
	for _, p := range v.list.Items() {
		if p.Reviewed {
			t.Error("Reviewed set with mark_reviewed off; default must match upstream")
		}
	}
}

func TestRenderReviewedTag(t *testing.T) {
	p := pr{Number: 7, Title: "fix the bug", Reviewed: true}
	p.Repository.NameWithOwner = "o/r"
	out := p.Render(80, false, ui.Highlighter{})
	if !strings.Contains(out, "· reviewed") {
		t.Errorf("reviewed row missing tag:\n%s", out)
	}
	p.Reviewed = false
	if strings.Contains(p.Render(80, false, ui.Highlighter{}), "· reviewed") {
		t.Error("unreviewed row carries the reviewed tag")
	}
}
