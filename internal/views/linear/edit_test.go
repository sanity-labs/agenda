package linear

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// editView serves a fake Linear and records the variables of each mutation.
func editView(t *testing.T) (*View, func() map[string]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string
			Variables map[string]any
		}
		_ = json.Unmarshal(b, &req)
		if req.Variables["after"] == nil && (strings.Contains(req.Query, "issueLabels") || strings.Contains(req.Query, "members(")) {
			teamFetches.Add(1)
		}
		if strings.Contains(req.Query, "issueLabels") {
			// Two pages, the second holding the group's children, so a missing
			// pagination loop drops them.
			if req.Variables["after"] == nil {
				_, _ = w.Write([]byte(`{"data":{"issueLabels":{"nodes":[
					{"id":"g1","name":"[SRE] Component","isGroup":true},
					{"id":"l1","name":"bug","color":"#f00"}],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"nodes":[
				{"id":"l2","name":"foo","color":"#00f","parent":{"id":"g1","name":"[SRE] Component"}},
				{"id":"l3","name":"foo","color":"#0f0","parent":{"id":"g2","name":"Other"}}],"pageInfo":{"hasNextPage":false}}}}`))
			return
		}
		if strings.Contains(req.Query, "members(") {
			p := `{"data":{"team":{"members":{"nodes":[{"id":"u2","displayName":"zed"},{"id":"u1","displayName":"me"}],"pageInfo":{"hasNextPage":true,"endCursor":"m1"}}}}}`
			if req.Variables["after"] != nil {
				p = `{"data":{"team":{"members":{"nodes":[{"id":"u3","displayName":"amy"}],"pageInfo":{"hasNextPage":false}}}}}`
			}
			_, _ = w.Write([]byte(p))
			return
		}
		for _, name := range []string{"issueUpdate", "commentCreate", "issueLabelCreate", "favoriteCreate"} {
			if strings.Contains(req.Query, "mutation") && strings.Contains(req.Query, name) {
				mu.Lock()
				got[name] = req.Variables
				mu.Unlock()
				_, _ = w.Write([]byte(`{"data":{"` + name + `":{"success":true,"issueLabel":{"id":"new1","name":"zzz"}}}}`))
				return
			}
		}
		as := ""
		if liveAssignee != "" {
			as = `"assignee":{"id":"` + liveAssignee + `"},`
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"},"issue":{"id":"uuid-1",` + as + `"team":{"id":"team-1","states":{"nodes":[]}}}}}`))
	}))
	t.Cleanup(srv.Close)
	old := endpoint
	endpoint = srv.URL
	t.Cleanup(func() { endpoint = old })

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)
	v.SetSize(90, 40, 20)
	i := issue{Identifier: "SRE-1", Title: "x"}
	i.Labels.Nodes = []label{{ID: "l1", Name: "bug", Color: "#f00"}}
	v.Update(loadedMsg{issues: []issue{i}})
	v.Update(v.PrepareCommands()())
	return v, func() map[string]map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func command(t *testing.T, v *View, id string) ui.Command {
	t.Helper()
	for _, c := range v.Commands() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %q command in %+v", id, v.Commands())
	return ui.Command{}
}

func TestLabelsMenuListsEverythingApplicable(t *testing.T) {
	v, _ := editView(t)
	var got []string
	for _, o := range command(t, v, "labels").Next().Options {
		got = append(got, o.Title)
	}
	want := "[SRE] Component / foo,bug (on),Other / foo"
	if strings.Join(got, ",") != want {
		t.Errorf("options = %q, want %q (second page included, group left out, children under their group)", got, want)
	}
}

func TestLabelsMenuTogglesOneLabelAtATime(t *testing.T) {
	v, sent := editView(t)
	m := command(t, v, "labels").Next()
	v.Update(m.Options[0].Run()())
	in, _ := sent()["issueUpdate"]["input"].(map[string]any)
	if ids, _ := in["addedLabelIds"].([]any); len(ids) != 1 || ids[0] != "l2" || in["removedLabelIds"] != nil || in["labelIds"] != nil {
		t.Errorf("adding sent %v, want only addedLabelIds [l2]", in)
	}
	if got := v.list.Selected().Labels.Nodes; len(got) != 2 {
		t.Errorf("row labels = %v, want bug and foo", got)
	}

	m = command(t, v, "labels").Next()
	if m.Options[0].Title != "[SRE] Component / foo (on)" || m.Options[2].Title != "Other / foo" {
		t.Fatalf("on is matched by id, so the same-named child stays off, got %+v", m.Options)
	}
	v.Update(m.Options[1].Run()())
	in, _ = sent()["issueUpdate"]["input"].(map[string]any)
	if ids, _ := in["removedLabelIds"].([]any); len(ids) != 1 || ids[0] != "l1" || in["addedLabelIds"] != nil {
		t.Errorf("removing sent %v, want only removedLabelIds [l1]", in)
	}
	if got := v.list.Selected().Labels.Nodes; len(got) != 1 || got[0].ID != "l2" {
		t.Errorf("row labels = %v, want only foo l2", got)
	}
}

func TestCreateLabelInAGroupThenApplyIt(t *testing.T) {
	v, sent := editView(t)
	m := command(t, v, "labels").Next()
	if len(ui.MatchCommands(m.Options, "zzz")) != 0 || m.Fallback == nil {
		t.Fatal("labels menu should offer a fallback for a query that matches nothing")
	}
	create := m.Fallback("zzz")
	if len(create) != 1 || create[0].Title != `Create label "zzz"` {
		t.Fatalf("fallback = %+v", create)
	}
	groups := create[0].Next()
	if len(groups.Options) != 2 || groups.Options[0].Title != "No group" || groups.Options[1].Title != "[SRE] Component" {
		t.Fatalf("group options = %+v, want No group then the group", groups.Options)
	}
	v.Update(groups.Options[1].Run()())
	in, _ := sent()["issueLabelCreate"]["input"].(map[string]any)
	if in["name"] != "zzz" || in["teamId"] != "team-1" || in["parentId"] != "g1" {
		t.Errorf("issueLabelCreate input = %v", in)
	}
	up, _ := sent()["issueUpdate"]["input"].(map[string]any)
	if ids, _ := up["addedLabelIds"].([]any); len(ids) != 1 || ids[0] != "new1" {
		t.Errorf("the new label should be applied to the issue, sent %v", up)
	}
}

func TestFavoriteCommandForTheShownProject(t *testing.T) {
	v, sent := editView(t)
	for _, c := range v.Commands() {
		if c.ID == "favorite" {
			t.Fatal("no favorite command while the source is not a project or initiative")
		}
	}
	v.source = navSource{Kind: "initiative", ProjectID: "i1", Label: "Init"}
	v.Update(command(t, v, "favorite").Run()())
	if in, _ := sent()["favoriteCreate"]["input"].(map[string]any); in["initiativeId"] != "i1" {
		t.Errorf("favoriteCreate input = %v", in)
	}
	v.favs = []navProject{{ID: "i1"}}
	for _, c := range v.Commands() {
		if c.ID == "favorite" {
			t.Error("already favorited, so not offered again")
		}
	}
}

func TestSearchResultForAProjectBecomesTheSource(t *testing.T) {
	ref := scopeRef("project", navProject{ID: "p1", Name: "Core: infra"})
	if !strings.HasPrefix(ref.Label, "\U000f01a7") {
		t.Errorf("project glyph = %q", ref.Label)
	}
	v, _ := editView(t)
	if cmd := v.FetchRef(ref.ID, ""); cmd == nil {
		t.Fatal("following a project result should refetch")
	}
	if want := (navSource{Kind: "project", ProjectID: "p1", Label: "Core: infra"}); v.source != want {
		t.Errorf("source = %+v, want %+v", v.source, want)
	}
}

func TestAddCommentPostsAndRefreshesComments(t *testing.T) {
	v, sent := editView(t)
	v.comments = map[string]*commentsState{"SRE-1": {done: true}}
	rev := v.commentsRev
	m := command(t, v, "comment").Next()
	if m.Text == nil {
		t.Fatal("Add comment should open a free-text level")
	}
	v.Update(m.Text("looks good")())
	in, _ := sent()["commentCreate"]["input"].(map[string]any)
	if in["issueId"] != "SRE-1" || in["body"] != "looks good" {
		t.Errorf("commentCreate input = %v", in)
	}
	if _, cached := v.comments["SRE-1"]; cached || v.commentsRev == rev {
		t.Errorf("comments should be dropped and the preview key bumped")
	}
	if n, _ := v.list.Selected().commentCount(); n != 1 {
		t.Errorf("comment count = %d, want 1", n)
	}
}

func TestSearchAddsProjectsAndInitiativesButNotToTheLinkPicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"searchIssues":{"nodes":[{"identifier":"SRE-2","title":"t"}]},
			"projects":{"nodes":[{"id":"p1","name":"Proj"}]},"initiatives":{"nodes":[{"id":"i1","name":"Init"}]}}}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL
	defer func() { endpoint = old }()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.LinearConfig{Token: "t"}, nil, nil, nil)

	res := v.Search("x", "", 5)().(ui.SearchResultsMsg)
	if len(res.Results) != 3 || res.Results[1].Detail != "Initiative" || res.Results[2].Detail != "Project" {
		t.Fatalf("results = %+v, want the issue, then initiative, then project", res.Results)
	}
	res = v.searchOthers("SRE-1", "x", 5)().(ui.SearchResultsMsg)
	if len(res.Results) != 1 {
		t.Errorf("link picker = %+v, want only the issue", res.Results)
	}
}

var teamFetches atomic.Int32

func TestTeamDataIsFetchedOncePerTeam(t *testing.T) {
	teamFetches.Store(0)
	v, _ := editView(t)
	delete(v.states, "SRE-1") // as if another issue of the same team were opened
	v.Update(v.PrepareCommands()())
	if n := teamFetches.Load(); n != 2 {
		t.Errorf("team labels and members queries = %d, want 2 (one each)", n)
	}
	if len(command(t, v, "assign").Next().Options) != 3 {
		t.Error("second issue lost the cached members")
	}
}

// liveAssignee is what the fake statesQuery reports for the issue.
var liveAssignee string

func TestAssignMenuUsesTheLiveAssigneeNotTheRow(t *testing.T) {
	liveAssignee = "u2"
	t.Cleanup(func() { liveAssignee = "" })
	v, sent := editView(t) // the row carries no assignee id
	m := command(t, v, "assign").Next()
	if m.Options[0].Title != "Unassign" || m.Options[3].Title != "zed (assigned)" {
		t.Fatalf("options = %+v, want Unassign first and zed marked", m.Options)
	}
	v.Update(m.Options[0].Run()())
	if in, _ := sent()["issueUpdate"]["input"].(map[string]any); in["assigneeId"] != nil {
		t.Fatalf("sent %v, want null", in)
	}
	if first := command(t, v, "assign").Next().Options[0].Title; first == "Unassign" {
		t.Error("reopened menu still offers Unassign after unassigning")
	}
}

func TestAssignMenuListsMembersAndAssigns(t *testing.T) {
	v, sent := editView(t)
	var got []string
	for _, o := range command(t, v, "assign").Next().Options {
		got = append(got, o.Title)
	}
	if want := "Me (me),amy,zed"; strings.Join(got, ",") != want {
		t.Fatalf("options = %q, want %q (second page in, Me first, no Unassign without an assignee)", got, want)
	}
	v.Update(command(t, v, "assign").Next().Options[2].Run()())
	in, _ := sent()["issueUpdate"]["input"].(map[string]any)
	if in["assigneeId"] != "u2" || v.list.Selected().Assignee.DisplayName != "zed" {
		t.Errorf("sent %v, row assignee %q, want zed (u2)", in, v.list.Selected().Assignee.DisplayName)
	}

	m := command(t, v, "assign").Next()
	if m.Options[0].Title != "Unassign" || m.Options[3].Title != "zed (assigned)" {
		t.Fatalf("options = %+v, want Unassign first and zed marked", m.Options)
	}
	v.Update(m.Options[3].Run()()) // the current assignee clears the assignment
	in, _ = sent()["issueUpdate"]["input"].(map[string]any)
	if v, ok := in["assigneeId"]; !ok || v != nil {
		t.Errorf("sent %v, want assigneeId null", in)
	}
	if v.list.Selected().Assignee.ID != "" {
		t.Error("row should be unassigned")
	}
}
