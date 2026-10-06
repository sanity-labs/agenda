package prs

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/herdr"
	"github.com/sanity-labs/agenda/internal/store"
)

func TestHerdrRequestUsesKnownLinkedIssues(t *testing.T) {
	st := store.New()
	st.PutIssues([]store.Issue{{Identifier: "SRE-5287", Title: "Tools staging"}})
	p := pr{Number: 12, Title: "SRE-5287: fix UTF-8 handling", URL: "u12", HeadRefName: "sre-5287-utf8"}
	p.Repository.NameWithOwner = "sanity-io/ops"

	v := New(config.GitHubConfig{}, nil, nil, st)
	v.SetSize(80, 60, 20)
	v.raw = []pr{p}
	v.applySort()
	v.SetHerdr(true)

	cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter in herdr mode sent no request")
	}
	req, ok := cmd().(herdr.Request)
	if !ok {
		t.Fatalf("enter sent %T, want a herdr.Request", cmd())
	}
	if len(req.Issues) != 1 || req.Issues[0] != (herdr.Issue{ID: "SRE-5287", Title: "Tools staging"}) {
		t.Errorf("issues = %+v, want only SRE-5287 with its title (UTF-8 is no Linear team)", req.Issues)
	}
	if req.PR == nil || *req.PR != (herdr.PR{Repo: "sanity-io/ops", Number: 12, Title: p.Title, Branch: "sre-5287-utf8"}) {
		t.Errorf("PR = %+v", req.PR)
	}
	if h := v.keys.Open.Help().Desc; h != "workspace" {
		t.Errorf("open's footer label = %q in herdr mode, want workspace", h)
	}
}
