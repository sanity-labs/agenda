package update

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]struct {
		want semver
		ok   bool
	}{
		"1.2.3":           {semver{1, 2, 3}, true},
		"v1.2.3":          {semver{1, 2, 3}, true},
		" v0.1.2 ":        {semver{0, 1, 2}, true},
		"1.2.3-rc1":       {semver{1, 2, 3}, true},
		"1.2.3+meta":      {semver{1, 2, 3}, true},
		"devel":           {semver{}, false},
		"devel (abc1234)": {semver{}, false},
		// A go-install pseudo-version is 0.0.0, below every real release, so
		// such a build correctly learns an update exists.
		"v0.0.0-2026-abcd": {semver{0, 0, 0}, true},
		"1.2":              {semver{}, false},
		"":                 {semver{}, false},
	}
	for in, want := range cases {
		got, ok := parse(in)
		if ok != want.ok || got != want.want {
			t.Errorf("parse(%q) = %v,%v want %v,%v", in, got, ok, want.want, want.ok)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b semver
		want bool
	}{
		{semver{0, 2, 0}, semver{0, 1, 9}, true},
		{semver{1, 0, 0}, semver{0, 9, 9}, true},
		{semver{0, 1, 3}, semver{0, 1, 2}, true},
		{semver{0, 1, 2}, semver{0, 1, 2}, false},
		{semver{0, 1, 1}, semver{0, 1, 2}, false},
		{semver{0, 9, 9}, semver{1, 0, 0}, false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A devel build has nothing to compare against, so it must never claim an
// update is available (and must not hit the network to decide that).
func TestCheckSkipsUnparseableCurrent(t *testing.T) {
	res := Check(t.Context(), "devel")
	if res.Available || res.Err != nil {
		t.Errorf("Check(devel) = %+v, want no update and no error", res)
	}
}
