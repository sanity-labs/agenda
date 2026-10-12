package ui

import (
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/foo.go b/foo.go
index 123..456 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main
+// added line
-// removed line
 func main() {}
`

func TestRenderDiff(t *testing.T) {
	out := RenderDiff(sampleDiff, 80)
	if !strings.Contains(out, "▸ foo.go") {
		t.Error("file header not promoted to ▸ path form")
	}
	// The raw prefixes survive (colored), so content is intact.
	for _, want := range []string{"+// added line", "-// removed line", "@@ -1,3 +1,4 @@"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered diff missing %q", want)
		}
	}
}

func TestRenderDiffTruncatesHugeDiffs(t *testing.T) {
	big := strings.Repeat("+x\n", maxDiffLines+100)
	out := RenderDiff(big, 80)
	if !strings.Contains(out, "diff truncated") {
		t.Error("huge diff not truncated")
	}
	if got := strings.Count(out, "\n"); got > maxDiffLines+5 {
		t.Errorf("truncated diff still has %d lines", got)
	}
}

func TestRenderAnnotatedDiffPinsThreads(t *testing.T) {
	anns := []DiffAnnotation{
		{ID: "t1", Path: "foo.go", Line: 2, Text: "┃ @alice: why?"},
		{ID: "t2", Path: "foo.go", Line: 0, Text: "┃ file-level note"},
		{ID: "t3", Path: "gone.go", Line: 9, Text: "┃ outdated note"},
	}
	out, anchors := RenderAnnotatedDiff(sampleDiff, 80, anns)
	lines := strings.Split(out, "\n")

	if len(anchors) != 3 {
		t.Fatalf("anchors = %d, want 3 (unplaced ones still anchor)", len(anchors))
	}
	byID := map[string]int{}
	for _, a := range anchors {
		byID[a.ID] = a.Line
	}
	// t2 pins under the file header, before t1.
	if byID["t2"] >= byID["t1"] {
		t.Errorf("file-level anchor at %d should precede line anchor at %d", byID["t2"], byID["t1"])
	}
	// t1 pins directly after right-side line 2 ("+// added line" is line 2:
	// hunk starts at 1 with " package main", then the addition).
	if got := lines[byID["t1"]]; !strings.Contains(got, "@alice") {
		t.Errorf("anchor line %d = %q, want the annotation", byID["t1"], got)
	}
	prev := lines[byID["t1"]-1]
	if !strings.Contains(prev, "added line") {
		t.Errorf("annotation pinned after %q, want after the added line", prev)
	}
	// t3's file isn't in the diff: it lands in the trailing section.
	if !strings.Contains(out, "outdated note") || !strings.Contains(out, "gone.go:9") {
		t.Error("unplaced annotation dropped instead of appended")
	}
}

func TestHunkNewStart(t *testing.T) {
	cases := map[string]int{
		"@@ -1,3 +1,4 @@":        1,
		"@@ -10,7 +12,8 @@ func": 12,
		"@@ -0,0 +1 @@":          1,
		"not a hunk":             0,
	}
	for in, want := range cases {
		if got := hunkNewStart(in); got != want {
			t.Errorf("hunkNewStart(%q) = %d, want %d", in, got, want)
		}
	}
}
