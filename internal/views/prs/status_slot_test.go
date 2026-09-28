package prs

import (
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// The list header and the footer both used to render statusText, so the
// counts and sort appeared twice on screen.
func TestStatusDoesNotRepeatTheHeader(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.Update(mineMsg{prs: []pr{{Number: 1, Title: "one"}}}) // clears loading
	v.SetSize(80, 40, 20)

	header := v.ListView()
	if !strings.Contains(header, "1 PRs") {
		t.Fatalf("list header lost the counts:\n%s", header)
	}
	if got := v.Status(); got != "" {
		t.Errorf("footer status = %q, want empty so the header is not repeated", got)
	}
}

// A flash is the action's result and has nowhere else to show, so it keeps
// the footer slot.
func TestStatusKeepsFlash(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	v := New(config.GitHubConfig{}, nil, nil, nil)
	v.flash = ui.Green.Render("✓ approved o/r#1")
	if !strings.Contains(v.Status(), "approved") {
		t.Errorf("footer status = %q, want the flash", v.Status())
	}
}
