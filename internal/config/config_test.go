package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	d := Default()
	if len(d.Views) != 3 {
		t.Errorf("default Views = %v, want 3 entries", d.Views)
	}
	if d.GitHub.Filter == "" {
		t.Error("default GitHub.Filter is empty")
	}
	if !d.SessionsEnabled() {
		t.Error("sessions should be enabled by default")
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty dir: no config.yml
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for missing file", err)
	}
	if cfg.GitHub.Filter != Default().GitHub.Filter {
		t.Errorf("GitHub.Filter = %q, want default", cfg.GitHub.Filter)
	}
}

func TestLoadMergesOntoDefaults(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "agenda")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only override a couple of fields; the rest must keep their defaults.
	yaml := "github:\n  filter: \"org:acme review-requested:@me\"\nlinear:\n  token: lin_api_secret\nsessions:\n  enabled: false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GitHub.Filter != "org:acme review-requested:@me" {
		t.Errorf("GitHub.Filter = %q, want overridden value", cfg.GitHub.Filter)
	}
	if cfg.Linear.Token != "lin_api_secret" {
		t.Errorf("Linear.Token = %q, want overridden value", cfg.Linear.Token)
	}
	if cfg.SessionsEnabled() {
		t.Error("sessions enabled, want disabled by file")
	}
	// Views weren't in the file, so they should still be the default.
	if len(cfg.Views) != 3 {
		t.Errorf("Views = %v, want default 3 entries (not clobbered)", cfg.Views)
	}
}

func writeConfig(t *testing.T, yaml string) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "agenda")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnabledViews(t *testing.T) {
	writeConfig(t, "linear:\n  enabled: false\ngithub:\n  enabled: true\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.EnabledViews()
	want := []string{"prs", "sessions"}
	if len(got) != len(want) {
		t.Fatalf("EnabledViews() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("EnabledViews()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// An unrecognised view name is dropped rather than crashing later wiring.
	cfg.Views = []string{"prs", "bogus"}
	if got := cfg.EnabledViews(); len(got) != 1 || got[0] != "prs" {
		t.Errorf("EnabledViews() with bogus name = %v, want [prs]", got)
	}
}

func TestRefreshResolution(t *testing.T) {
	writeConfig(t, "refresh:\n  every: 5m\n  linear: 90s\n  sessions: \"0\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RefreshFor("prs"); got != 5*time.Minute {
		t.Errorf("RefreshFor(prs) = %v, want 5m (global)", got)
	}
	if got := cfg.RefreshFor("linear"); got != 90*time.Second {
		t.Errorf("RefreshFor(linear) = %v, want 90s (override)", got)
	}
	if got := cfg.RefreshFor("sessions"); got != 0 {
		t.Errorf("RefreshFor(sessions) = %v, want 0 (explicitly off)", got)
	}
}

func TestRefreshRejectsBadDuration(t *testing.T) {
	writeConfig(t, "refresh:\n  every: sometimes\n")
	if _, err := Load(); err == nil {
		t.Error("Load() = nil error, want parse failure for bad duration")
	}
}

func TestKeymapOf(t *testing.T) {
	writeConfig(t, "keys:\n  global:\n    quit: x\n    zoom: [z, Z]\n    refresh: []\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Keys.Of("global", "quit", "q", "ctrl+c"); len(got) != 1 || got[0] != "x" {
		t.Errorf("Of(quit) = %v, want [x] (scalar form)", got)
	}
	if got := cfg.Keys.Of("global", "zoom", "z"); len(got) != 2 || got[1] != "Z" {
		t.Errorf("Of(zoom) = %v, want [z Z] (list form)", got)
	}
	if got := cfg.Keys.Of("global", "refresh", "ctrl+r"); len(got) != 0 {
		t.Errorf("Of(refresh) = %v, want empty (explicitly disabled)", got)
	}
	if got := cfg.Keys.Of("global", "unset", "d"); len(got) != 1 || got[0] != "d" {
		t.Errorf("Of(unset) = %v, want the default", got)
	}
	if got := cfg.Keys.Of("prs", "open", "enter"); len(got) != 1 || got[0] != "enter" {
		t.Errorf("Of on absent scope = %v, want the default", got)
	}
}

func TestNotifyDefaults(t *testing.T) {
	var n NotifyConfig
	if n.Enabled() || n.SoundEnabled() {
		t.Error("notifications should be fully off by default")
	}
	writeConfig(t, "notifications:\n  popup: terminal\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notify.Enabled() || cfg.Notify.Popup != "terminal" {
		t.Errorf("popup = %q, want terminal enabled", cfg.Notify.Popup)
	}
	if !cfg.Notify.SoundEnabled() {
		t.Error("sound should default on when the popup is on")
	}
	writeConfig(t, "notifications:\n  popup: desktop\n  sound: false\n")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify.SoundEnabled() {
		t.Error("sound explicitly off, but reported on")
	}
}

func TestLinearFilterDefaults(t *testing.T) {
	writeConfig(t, "linear:\n  filter:\n    include_completed: true\n    teams: [SRE]\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Linear.Filter
	if !f.IncludeCompleted || f.IncludeCanceled {
		t.Errorf("filter = %+v, want completed included, canceled excluded", f)
	}
	if f.Limit != 100 {
		t.Errorf("Limit = %d, want backfilled default 100", f.Limit)
	}
}

func TestPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := "/custom/xdg/agenda/config.yml"; got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestLegacyScalarLinearFilterStillLoads(t *testing.T) {
	// The pre-struct form was a raw GraphQL clause string (never consumed);
	// upgrading with one in place must not break startup.
	writeConfig(t, "linear:\n  token: lin_api_x\n  filter: \"state: { type: { eq: started } }\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with legacy scalar filter = %v, want nil", err)
	}
	if cfg.Linear.Filter.Limit != 100 {
		t.Errorf("Limit = %d, want backfilled 100", cfg.Linear.Filter.Limit)
	}
}

func TestLinearLimitClamped(t *testing.T) {
	writeConfig(t, "linear:\n  filter:\n    limit: 500\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.Filter.Limit != 250 {
		t.Errorf("Limit = %d, want clamped to 250", cfg.Linear.Filter.Limit)
	}
}

func TestUpdateCheckDefaultsOn(t *testing.T) {
	if !Default().UpdateCheckEnabled() {
		t.Error("UpdateCheckEnabled() false by default, want on")
	}
	writeConfig(t, "update_check: false\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.UpdateCheckEnabled() {
		t.Error("update_check: false did not disable the check")
	}
}
