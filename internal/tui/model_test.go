package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// stubView is the minimal View for chrome-level tests.
type stubView struct{ title string }

func (s *stubView) Title() string           { return s.title }
func (s *stubView) Init() tea.Cmd           { return nil }
func (s *stubView) Update(tea.Msg) tea.Cmd  { return nil }
func (s *stubView) SetSize(int, int, int)   {}
func (s *stubView) ListView() string        { return "" }
func (s *stubView) PreviewView() string     { return "" }
func (s *stubView) Bindings() []key.Binding { return nil }
func (s *stubView) Status() string          { return "" }
func (s *stubView) InputActive() bool       { return false }
func (s *stubView) PreviewKey() string      { return "" }
func (s *stubView) Loading() bool           { return false }

func TestRefreshIntervalsMatchViewsByTitle(t *testing.T) {
	cfg := config.Default()
	every := config.Duration(5 * time.Minute)
	linear := config.Duration(90 * time.Second)
	cfg.Refresh = config.RefreshConfig{Every: every, Linear: &linear}

	views := []View{&stubView{"PRs"}, &stubView{"Sessions"}, &stubView{"Linear"}}
	got := refreshIntervals(cfg, views)
	want := []time.Duration{5 * time.Minute, 5 * time.Minute, 90 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("interval[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestViewIndexForKey(t *testing.T) {
	cases := map[string]int{
		"1":  0, // "1" jumps to the first view
		"2":  1,
		"5":  4, // mid-range exercises the s[0]-'1' formula
		"9":  8,
		"0":  -1, // 0 is not a view hotkey
		"a":  -1,
		"":   -1,
		"12": -1, // multi-char (e.g. a key name) is not a digit jump
	}
	for in, want := range cases {
		if got := viewIndexForKey(in); got != want {
			t.Errorf("viewIndexForKey(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestPreviewHiddenGivesListFullWidth(t *testing.T) {
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 120, 40, true

	listW, _, _ := m.dims()
	if listW >= 120 {
		t.Fatalf("baseline listW = %d, want < 120 (preview visible)", listW)
	}

	m.previewHidden = true
	listW, _, _ = m.dims()
	if listW != 120 {
		t.Errorf("hidden listW = %d, want full width 120", listW)
	}

	// Zoom wins over hidden: the preview comes back at full width.
	m.zoomed = true
	listW, prevW, _ := m.dims()
	if listW != 0 || prevW <= 0 {
		t.Errorf("zoomed+hidden = (list %d, preview %d), want (0, >0)", listW, prevW)
	}
}

func TestHidePreviewConfigSetsStartupState(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	m := New(cfg, []View{&stubView{"PRs"}})
	if !m.previewHidden {
		t.Error("HidePreview config did not set previewHidden at startup")
	}
}

func TestRevealPreviewMsgUnhidesPreview(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	m := New(cfg, []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 120, 40, true

	got, _ := m.Update(ui.RevealPreviewMsg{})
	if got.(Model).previewHidden {
		t.Error("previewHidden still true after RevealPreviewMsg")
	}
}

func TestTransientRevealConcealsOnMsg(t *testing.T) {
	cfg := config.Default()
	cfg.HidePreview = true
	m := New(cfg, []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 120, 40, true

	got, _ := m.Update(ui.RevealPreviewMsg{})
	m = got.(Model)
	if m.previewHidden || !m.previewTransient {
		t.Fatalf("after reveal: hidden=%v transient=%v, want false/true", m.previewHidden, m.previewTransient)
	}
	got, _ = m.Update(ui.ConcealPreviewMsg{})
	m = got.(Model)
	if !m.previewHidden || m.previewTransient {
		t.Errorf("after conceal: hidden=%v transient=%v, want true/false", m.previewHidden, m.previewTransient)
	}
}

func TestConcealIgnoredForDeliberateShow(t *testing.T) {
	// Preview visible because the user wants it visible (default config):
	// a conceal from a view must not hide it.
	m := New(config.Default(), []View{&stubView{"PRs"}})
	m.width, m.height, m.ready = 120, 40, true

	got, _ := m.Update(ui.ConcealPreviewMsg{})
	m = got.(Model)
	if m.previewHidden {
		t.Error("conceal hid a deliberately-visible preview")
	}
}

func TestVersionTagShowsUpdateArrow(t *testing.T) {
	m := New(config.Default(), []View{&stubView{"PRs"}}).WithVersion("0.1.2")
	if got := m.versionTag(); !strings.Contains(got, "v0.1.2") {
		t.Errorf("versionTag() = %q, want it to carry v0.1.2", got)
	}
	if strings.Contains(m.versionTag(), "↑") {
		t.Error("arrow shown with no newer release known")
	}

	m.newer = "0.2.0"
	got := m.versionTag()
	if !strings.Contains(got, "↑v0.2.0") {
		t.Errorf("versionTag() = %q, want the ↑v0.2.0 hint", got)
	}

	// A build with no version (devel) renders nothing rather than "v".
	if tag := New(config.Default(), []View{&stubView{"PRs"}}).versionTag(); tag != "" {
		t.Errorf("versionTag() with empty version = %q, want empty", tag)
	}
}

func TestTabBarCarriesVersionTag(t *testing.T) {
	m := New(config.Default(), []View{&stubView{"PRs"}}).WithVersion("0.1.2")
	m.width, m.height, m.ready = 120, 40, true
	m.newer = "0.2.0"

	tabs := m.renderTabs()
	if !strings.Contains(tabs, "v0.1.2") || !strings.Contains(tabs, "↑v0.2.0") {
		t.Errorf("tab bar missing version tag:\n%s", tabs)
	}
	if w := lipgloss.Width(strings.Split(tabs, "\n")[0]); w > 120 {
		t.Errorf("tab bar width = %d, want <= 120", w)
	}
}

func TestTabBarDropsVersionWhenNarrow(t *testing.T) {
	m := New(config.Default(), []View{&stubView{"PRs"}}).WithVersion("0.1.2")
	m.width, m.height, m.ready = 20, 40, true
	tabs := m.renderTabs()
	if w := lipgloss.Width(strings.Split(tabs, "\n")[0]); w > 20 {
		t.Errorf("narrow tab bar width = %d, want <= 20", w)
	}
}
