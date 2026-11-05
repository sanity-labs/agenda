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
	if len(cmds) != 1 || cmds[0].Title != "Status: Done" {
		t.Fatalf("commands = %+v, want only Status: Done (current state left out)", cmds)
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
