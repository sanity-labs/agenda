package linear

import (
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/ui"
)

func TestRenderCommentsRuleBetweenComments(t *testing.T) {
	v := &View{comments: map[string]*commentsState{
		"SRE-1": {done: true, list: make([]issueComment, 3)},
	}}
	rules := func() int { return strings.Count(v.renderComments("SRE-1", 40), strings.Repeat("─", 30)) }
	if n := rules(); n != 0 {
		t.Errorf("rule off: %d rules, want 0", n)
	}
	ui.SetCommentRule(true)
	defer ui.SetCommentRule(false)
	if n := rules(); n != 2 {
		t.Errorf("rule on: %d rules, want 2", n)
	}
}
