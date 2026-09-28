package config

import "testing"

func TestUnknownKeysReported(t *testing.T) {
	writeConfig(t, "notifications:\n  enabled: true\n  desktop: false\ntheme:\n  markdown: standard\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want a warning rather than a failure", err)
	}
	want := map[string]bool{"enabled": true, "desktop": true, "markdown": true}
	if len(cfg.Unknown) != 3 {
		t.Fatalf("Unknown = %v, want the three dead keys", cfg.Unknown)
	}
	for _, k := range cfg.Unknown {
		if !want[k] {
			t.Errorf("unexpected key %q in %v", k, cfg.Unknown)
		}
	}
}

// A clean config reports nothing.
func TestUnknownKeysEmptyForValidConfig(t *testing.T) {
	writeConfig(t, "notifications:\n  popup: terminal\ngithub:\n  page_size: 30\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Unknown) != 0 {
		t.Errorf("Unknown = %v, want empty for a valid config", cfg.Unknown)
	}
	if cfg.GitHub.ResolvedPageSize() != 30 {
		t.Errorf("page size = %d, want 30", cfg.GitHub.ResolvedPageSize())
	}
}
