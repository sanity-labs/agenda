package sessions

import (
	"slices"
	"testing"

	"github.com/sanity-labs/agenda/internal/herdr"
)

func TestHerdrRequestResumesWithSessionTool(t *testing.T) {
	v := &View{}
	v.list.SetRowHeight(2)
	v.list.SetSize(40, 20)
	s := mkSess("/a", "codex", 1)
	s.SessionID = "abc"
	v.raw = []session{s}
	v.applyView()
	v.SetHerdr(true)

	req, ok := v.Activate()().(herdr.Request)
	if !ok || req.Session == nil {
		t.Fatal("a double-click in herdr mode sent no session request")
	}
	if req.Session.ID != "abc" || req.Session.Cwd != "/a" || !slices.Equal(req.Session.Resume, []string{"codex", "resume", "abc"}) {
		t.Errorf("session = %+v", *req.Session)
	}
}
