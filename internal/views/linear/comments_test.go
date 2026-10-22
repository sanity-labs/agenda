package linear

import (
	"strings"
	"testing"
)

func TestRenderCommentsRuleBetweenComments(t *testing.T) {
	v := &View{comments: map[string]*commentsState{
		"SRE-1": {done: true, list: make([]issueComment, 3)},
	}}
	rules := func() int { return strings.Count(v.renderComments("SRE-1", 40), strings.Repeat("─", 30)) }
	if n := rules(); n != 2 {
		t.Errorf("%d rules, want 2", n)
	}
}
