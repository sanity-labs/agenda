package config

import "testing"

func TestSummaryLinesZeroDisables(t *testing.T) {
	if got := Default().GitHub.SummaryLines; got != 10 {
		t.Errorf("default = %d, want 10", got)
	}
	writeConfig(t, "github:\n  summary_lines: 0\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHub.SummaryLines != 0 {
		t.Errorf("explicit 0 = %d, want 0 (truncation off); the default is overwriting it",
			cfg.GitHub.SummaryLines)
	}
}
