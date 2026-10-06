package linear

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/herdr"
	"github.com/sanity-labs/agenda/internal/store"
)

func TestHerdrRequestCarriesOpenPR(t *testing.T) {
	st := store.New()
	st.PutPRs([]store.PR{{URL: "https://github.com/sanity-io/ops/pull/9", Branch: "sre-7-tidy"}})
	v := New(config.LinearConfig{Token: "x"}, nil, nil, st)
	v.SetSize(80, 60, 20)

	iss := issue{Identifier: "SRE-7", Title: "Tidy", BranchName: "obliadp/sre-7-tidy"}
	iss.Attachments.Nodes = []attachment{
		{URL: "https://github.com/sanity-io/ops/pull/3", Title: "old attempt"},
		{URL: "https://github.com/sanity-io/ops/pull/9", Title: "Tidy up"},
	}
	iss.Attachments.Nodes[0].Metadata.Status = "merged"
	iss.Attachments.Nodes[1].Metadata.Status = "inReview"
	v.raw = []issue{iss}
	v.applySort()
	v.SetHerdr(true)

	req, ok := v.Activate()().(herdr.Request)
	if !ok {
		t.Fatal("a double-click in herdr mode sent no request")
	}
	if len(req.Issues) != 1 || req.Issues[0] != (herdr.Issue{ID: "SRE-7", Title: "Tidy", Branch: "obliadp/sre-7-tidy"}) {
		t.Errorf("issues = %+v", req.Issues)
	}
	want := herdr.PR{Repo: "sanity-io/ops", Number: 9, Title: "Tidy up", Branch: "sre-7-tidy"}
	if req.PR == nil || *req.PR != want {
		t.Errorf("PR = %+v, want the open one (#9, not merged #3) with its branch from the store", req.PR)
	}
}

func TestHerdrEnterRequestsWorkspaceAndOOpensBrowser(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.SetSize(80, 60, 20)
	v.raw = []issue{{Identifier: "SRE-9", Title: "Thing", URL: "https://linear.app/x/issue/SRE-9"}}
	v.applySort()
	v.SetHerdr(true)

	cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter in herdr mode did nothing")
	}
	if req, ok := cmd().(herdr.Request); !ok || len(req.Issues) != 1 || req.Issues[0].ID != "SRE-9" {
		t.Errorf("enter sent %#v, want a herdr request for SRE-9", cmd())
	}
	if h := v.keys.Open.Help().Desc; h != "workspace" {
		t.Errorf("open's footer label = %q in herdr mode, want workspace", h)
	}
	if cmd := v.Update(tea.KeyPressMsg{Code: 'O', Text: "O"}); cmd == nil {
		t.Error("O did nothing; in herdr mode it opens the issue in the browser")
	}
}
