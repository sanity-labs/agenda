package prs

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func intp(i int) *int { return &i }

func mkThread(id, path string, line *int, resolved, outdated bool, bodies ...string) prThread {
	t := prThread{ID: id, Path: path, Line: line, IsResolved: resolved, IsOutdated: outdated}
	for i, b := range bodies {
		c := prComment{Body: b, CreatedAt: time.Now().Add(time.Duration(i) * time.Minute)}
		c.Author.Login = "alice"
		t.Comments.Nodes = append(t.Comments.Nodes, c)
	}
	return t
}

func TestThreadAnnotations(t *testing.T) {
	anns := threadAnnotations([]prThread{
		mkThread("t1", "foo.go", intp(42), false, false, "why this?"),
		mkThread("t2", "bar.go", nil, true, true, "old note"),
	}, 80, nil)

	if anns[0].Path != "foo.go" || anns[0].Line != 42 {
		t.Errorf("ann[0] pinned at %s:%d, want foo.go:42", anns[0].Path, anns[0].Line)
	}
	if !strings.Contains(anns[0].Text, "@alice") || !strings.Contains(anns[0].Text, "why this?") {
		t.Errorf("ann text missing author/body: %q", anns[0].Text)
	}
	// Outdated threads pin at the file header (line 0).
	if anns[1].Line != 0 {
		t.Errorf("outdated thread pinned at line %d, want 0", anns[1].Line)
	}
	if !strings.Contains(anns[1].Text, "resolved") {
		t.Errorf("resolved thread not marked: %q", anns[1].Text)
	}
}

func TestRenderCommentsPane(t *testing.T) {
	var data prComments
	c := prComment{Body: "first!", CreatedAt: time.Now().Add(-2 * time.Hour)}
	c.Author.Login = "bob"
	data.Comments.Nodes = []prComment{c}
	r := prReview{State: "APPROVED", Body: "ship it", CreatedAt: time.Now().Add(-time.Hour)}
	r.Author.Login = "carol"
	data.Reviews.Nodes = []prReview{r}
	// Empty COMMENTED shell reviews (containers for inline threads) are hidden.
	shell := prReview{State: "COMMENTED", CreatedAt: time.Now()}
	shell.Author.Login = "dave"
	data.Reviews.Nodes = append(data.Reviews.Nodes, shell)
	data.ReviewThreads.Nodes = []prThread{mkThread("t1", "foo.go", intp(3), false, false, "hm")}

	out, anchors := renderCommentsPane(data, 60, nil)
	if !strings.Contains(out, "@bob") || !strings.Contains(out, "first") {
		t.Error("conversation comment missing")
	}
	if !strings.Contains(out, "@carol") || !strings.Contains(out, "approved") {
		t.Error("review verdict missing")
	}
	if strings.Contains(out, "@dave") {
		t.Error("empty COMMENTED shell review should be hidden")
	}
	if len(anchors) != 1 || anchors[0].ID != "t1" {
		t.Fatalf("anchors = %v, want one for t1", anchors)
	}
	// The anchor points at the thread's header line.
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[anchors[0].Line], "foo.go:3") {
		t.Errorf("anchor line = %q, want the thread header", lines[anchors[0].Line])
	}
}

// In the comments pane a resolved thread is one line until unfolded; the
// unfolded map opens it with its body.
func TestCommentsPaneFoldsResolvedThreads(t *testing.T) {
	var data prComments
	data.ReviewThreads.Nodes = []prThread{
		mkThread("t1", "foo.go", intp(3), true, false, "nit", "done"),
		mkThread("t2", "bar.go", intp(5), false, false, "still open"),
	}
	raw, anchors := renderCommentsPane(data, 60, nil)
	out := ansi.Strip(raw)
	if strings.Contains(out, "nit") || !strings.Contains(out, "2 comments · click to read") {
		t.Errorf("resolved thread not folded:\n%s", out)
	}
	if !strings.Contains(out, "still open") {
		t.Errorf("an open thread was folded too:\n%s", out)
	}
	if len(anchors) != 2 {
		t.Errorf("anchors = %v, want one per thread, folded or not", anchors)
	}
	raw, _ = renderCommentsPane(data, 60, map[string]bool{"t1": true})
	out = ansi.Strip(raw)
	if !strings.Contains(out, "nit") || !strings.Contains(out, "done") {
		t.Errorf("unfolded thread missing its body:\n%s", out)
	}
	// The unified diff's annotation folds the same way.
	anns := threadAnnotations(data.ReviewThreads.Nodes, 80, nil)
	if strings.Contains(anns[0].Text, "nit") || !strings.Contains(anns[0].Text, "resolved") {
		t.Errorf("diff annotation not folded: %q", anns[0].Text)
	}
}
