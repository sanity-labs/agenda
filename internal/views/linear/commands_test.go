package linear

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
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

	if c := v.Commands(); len(c) != 2 || c[1].ID != "link" {
		t.Fatalf("before the states are known only Link related issue is offered, got %+v", c)
	}
	v.Update(v.PrepareCommands()())
	cmds := v.Commands()
	if len(cmds) != 4 || cmds[0].Title != "Status" || cmds[1].ID != "priority" || cmds[2].ID != "link" || cmds[3].ID != "subscribe" {
		t.Fatalf("commands = %+v, want Status, Priority, Link related issue, subscribe", cmds)
	}
	menu := cmds[0].Next()
	if len(menu.Options) != 2 || menu.Options[0].Title != "Todo (current)" || menu.Options[1].Title != "Done" {
		t.Fatalf("status options = %+v, want Todo (current) then Done", menu.Options)
	}
	v.Update(menu.Options[1].Run()())
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

	last := func() ui.Command { c := v.Commands(); return c[len(c)-1] }
	if c := last(); c.Title != "Subscribe to issue" {
		t.Fatalf("last command = %+v, want Subscribe to issue", c)
	}
	v.Update(last().Run()())
	if c := last(); c.Title != "Unsubscribe from issue" {
		t.Fatalf("last command = %+v, want Unsubscribe from issue", c)
	}
	v.Update(last().Run()())
	mu.Lock()
	defer mu.Unlock()
	if len(mutations) != 2 || !strings.Contains(mutations[0], "issueSubscribe") ||
		!strings.Contains(mutations[1], "issueUnsubscribe") || !strings.Contains(mutations[0], `"id":"uuid-1"`) {
		t.Errorf("mutations = %v", mutations)
	}
}

func TestPriorityChangeRefreshesDetail(t *testing.T) {
	v := New(config.LinearConfig{Token: "x"}, nil, nil, nil)
	v.raw = mkIDs("A-1")
	v.applySort()
	before := v.PreviewKey()
	v.Update(prioritySetMsg{ident: "A-1", to: 1})
	if v.PreviewKey() == before {
		t.Fatalf("PreviewKey unchanged after a priority change: detail keeps the old priority")
	}
}

func TestPriorityCommandSetsPriority(t *testing.T) {
	var mu sync.Mutex
	var mutation string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		mutation = string(b)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL
	defer func() { endpoint = old }()

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	i := issue{Identifier: "SRE-1", Title: "x", Priority: 3}
	v.Update(loadedMsg{issues: []issue{i}})

	var cmd ui.Command
	for _, c := range v.Commands() {
		if c.ID == "priority" {
			cmd = c
		}
	}
	if cmd.Title == "" || !strings.HasPrefix(cmd.Detail, "Medium") {
		t.Fatalf("priority command = %+v, want a row whose detail starts with the current priority", cmd)
	}
	menu := cmd.Next()
	if len(menu.Options) != 5 || menu.Options[3].Title != "Medium (current)" || menu.Options[1].Title != "Urgent" {
		t.Fatalf("options = %+v", menu.Options)
	}
	v.Update(menu.Options[1].Run()())
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(mutation, `"priority":1`) || !strings.Contains(mutation, `"id":"SRE-1"`) {
		t.Errorf("mutation = %s", mutation)
	}
	if got := v.list.Selected().Priority; got != 1 {
		t.Errorf("row priority = %d, want 1", got)
	}
}
