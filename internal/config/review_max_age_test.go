package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseDurationDaysAndWeeks(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"1d":    24 * time.Hour,
		"2w":    14 * 24 * time.Hour,
		"1w2d":  9 * 24 * time.Hour,
		"1d12h": 36 * time.Hour,
		"1.5d":  36 * time.Hour,
		"1m":    time.Minute,
		"90s":   90 * time.Second,
	} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "xd", "1d2", "-1d", "12h1d"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) accepted", in)
		}
	}
}

func TestReviewMaxAgeFromYAML(t *testing.T) {
	var g GitHubConfig
	if err := yaml.Unmarshal([]byte("review_max_age: 30d\n"), &g); err != nil {
		t.Fatal(err)
	}
	if time.Duration(g.ReviewMaxAge) != 30*24*time.Hour {
		t.Fatalf("review_max_age = %v, want 720h", time.Duration(g.ReviewMaxAge))
	}
}

func TestReviewQueryMaxAge(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 37, 0, 0, time.UTC)
	g := GitHubConfig{ReviewFilter: "review-requested:@me is:open"}
	if q := g.reviewQueryAt(now); q != g.ReviewFilter {
		t.Fatalf("unset max age changed the query: %q", q)
	}
	g.ReviewMaxAge = Duration(7 * 24 * time.Hour)
	g.HideDependencyBots = true
	want := "review-requested:@me is:open -author:app/renovate -author:app/dependabot updated:>=2026-11-05T10:00:00Z"
	if q := g.reviewQueryAt(now); q != want {
		t.Fatalf("query = %q\nwant    %q", q, want)
	}
	// Within the same hour the query must not move, or a page cursor issued
	// for one fetch would be replayed against a different search.
	if q := g.reviewQueryAt(now.Add(20 * time.Minute)); q != want {
		t.Fatalf("query moved within the hour: %q", q)
	}
}
