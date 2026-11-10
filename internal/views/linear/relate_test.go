package linear

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// Every wording maps to one stored direction: "blocked by" and "duplicated
// by" are the same relation as their twins with the issues swapped.
func TestLinkRelatedFlowSendsTheRightMutation(t *testing.T) {
	cases := []struct{ kind, from, to, api string }{
		{"blocks", "SRE-1", "SRE-9", "blocks"},
		{"blocked by", "SRE-9", "SRE-1", "blocks"},
		{"related to", "SRE-1", "SRE-9", "related"},
		{"duplicate of", "SRE-1", "SRE-9", "duplicate"},
		{"duplicated by", "SRE-9", "SRE-1", "duplicate"},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			var mu sync.Mutex
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				if strings.Contains(string(b), "issueRelationCreate") {
					var req struct {
						Variables struct{ Input map[string]any }
					}
					_ = json.Unmarshal(b, &req)
					mu.Lock()
					got = req.Variables.Input
					mu.Unlock()
					_, _ = w.Write([]byte(`{"data":{"issueRelationCreate":{"success":true}}}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"issue":{"identifier":"SRE-1"}}}`))
			}))
			defer srv.Close()
			old := endpoint
			endpoint = srv.URL
			defer func() { endpoint = old }()

			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
			v.SetSize(90, 40, 20)
			v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1", Title: "x"}}})

			var link ui.Command
			for _, cmd := range v.Commands() {
				if cmd.ID == "link" {
					link = cmd
				}
			}
			var kind ui.Command
			for _, o := range link.Next().Options {
				if o.Title == c.kind {
					kind = o
				}
			}
			if kind.Next == nil {
				t.Fatalf("no %q option", c.kind)
			}
			picker := kind.Next()
			if picker.Search == nil || picker.Pick == nil {
				t.Fatal("third level should be a search picker")
			}
			v.Update(picker.Pick(ui.Ref{ID: "SRE-9"})())
			mu.Lock()
			defer mu.Unlock()
			if got["issueId"] != c.from || got["relatedIssueId"] != c.to || got["type"] != c.api {
				t.Errorf("input = %v, want %s -> %s as %s", got, c.from, c.to, c.api)
			}
			if _, ok := v.related["SRE-1"]; !ok {
				t.Error("related list was not refetched after linking")
			}
		})
	}
}

func TestLinkRelatedMatchesLooseWords(t *testing.T) {
	cmds := []ui.Command{{Title: "Link related issue", Aliases: []string{"mark", "relate", "relation", "block", "duplicate"}}}
	for _, q := range []string{"mark", "link", "relate", "relation", "block", "duplicate"} {
		if len(ui.MatchCommands(cmds, q)) != 1 {
			t.Errorf("%q should match Link related issue", q)
		}
	}
	if len(ui.MatchCommands(cmds, "merge")) != 0 {
		t.Error("merge should not match")
	}
}

// The picker offers every other issue, never the one being linked.
func TestLinkPickerLeavesOutTheIssueItself(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"searchIssues":{"nodes":[{"identifier":"SRE-1","title":"me"},{"identifier":"SRE-2","title":"other"}]}}}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL
	defer func() { endpoint = old }()
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	res := v.searchOthers("SRE-1", "sre", 10)().(ui.SearchResultsMsg)
	if len(res.Results) != 1 || res.Results[0].ID != "SRE-2" {
		t.Errorf("results = %+v, want only SRE-2", res.Results)
	}
}
