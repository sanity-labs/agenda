package linear

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
)

func TestStatusCommandsFromTeamStates(t *testing.T) {
	var mu sync.Mutex
	var mutation string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "issueUpdate") {
			mu.Lock()
			mutation = string(b)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"issue":{"id":"uuid-1","team":{"states":{"nodes":[
			{"id":"s2","name":"Done","type":"completed","color":"#0f0","position":2},
			{"id":"s1","name":"Todo","type":"unstarted","color":"#ccc","position":1}]}}}}}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL
	defer func() { endpoint = old }()

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	i := issue{Identifier: "SRE-1", Title: "x"}
	i.State.Name = "Todo"
	v.Update(loadedMsg{issues: []issue{i}})

	if len(v.Commands()) != 0 {
		t.Fatal("no commands before the states are known")
	}
	v.Update(v.PrepareCommands()())
	cmds := v.Commands()
	if len(cmds) != 2 || cmds[0].Title != "Status: Done" || cmds[1].ID != "subscribe" {
		t.Fatalf("commands = %+v, want Status: Done (current state left out) then subscribe", cmds)
	}
	v.Update(cmds[0].Run()())
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(mutation, `"id":"uuid-1"`) || !strings.Contains(mutation, `"stateId":"s2"`) {
		t.Errorf("mutation = %s", mutation)
	}
	if got := v.list.Selected().State.Name; got != "Done" {
		t.Errorf("row state = %q, want Done", got)
	}
}

func TestStatusChangeRefreshesDetail(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.raw = mkIDs("A-1")
	v.applySort()
	before := v.PreviewKey()
	v.Update(stateSetMsg{ident: "A-1", to: workflowState{Name: "Done", Type: "completed"}})
	if v.PreviewKey() == before {
		t.Fatalf("PreviewKey unchanged after a status change: detail keeps the old status")
	}
}

func TestSubscribeCommandToggles(t *testing.T) {
	var mu sync.Mutex
	var mutations []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "mutation") {
			mu.Lock()
			mutations = append(mutations, string(b))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":{"issueSubscribe":{"success":true},"issueUnsubscribe":{"success":true}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"me"},"issue":{"id":"uuid-1","subscribers":{"nodes":[{"id":"other"}]},"team":{"states":{"nodes":[]}}}}}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL
	defer func() { endpoint = old }()

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	v.Update(loadedMsg{issues: []issue{{Identifier: "SRE-1", Title: "x"}}})
	v.Update(v.PrepareCommands()())

	cmds := v.Commands()
	if len(cmds) != 1 || cmds[0].Title != "Subscribe to issue" {
		t.Fatalf("commands = %+v, want Subscribe to issue", cmds)
	}
	v.Update(cmds[0].Run()())
	cmds = v.Commands()
	if len(cmds) != 1 || cmds[0].Title != "Unsubscribe from issue" {
		t.Fatalf("commands = %+v, want Unsubscribe from issue", cmds)
	}
	v.Update(cmds[0].Run()())
	mu.Lock()
	defer mu.Unlock()
	if len(mutations) != 2 || !strings.Contains(mutations[0], "issueSubscribe") ||
		!strings.Contains(mutations[1], "issueUnsubscribe") || !strings.Contains(mutations[0], `"id":"uuid-1"`) {
		t.Errorf("mutations = %v", mutations)
	}
}
