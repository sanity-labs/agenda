package config

import (
	"strings"
	"testing"
)

// Reserved keys cannot be rebound: they are how you get out of any state,
// so a keymap that claims one would be unrecoverable without editing the
// config by hand.
func TestReservedKeysCannotBeRebound(t *testing.T) {
	k := Keymap{"prs": {"jobs": Chord{"left"}, "diff": Chord{"D", "esc"}}}

	if got := k.Of("prs", "jobs", "t"); len(got) != 0 {
		t.Errorf("a binding claiming left resolved to %v, want nothing", got)
	}
	// The rest of a partly-reserved binding still works: dropping the whole
	// override would lose a key the user legitimately asked for.
	if got := k.Of("prs", "diff", "d"); len(got) != 1 || got[0] != "D" {
		t.Errorf("binding = %v, want just the non-reserved key", got)
	}
}

// And the user is told, or the key keeps its built-in meaning and the
// override looks like it simply did not work.
func TestReservedKeysWarn(t *testing.T) {
	k := Keymap{"prs": {"jobs": Chord{"left"}}}
	warnings := k.reservedWarnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one", warnings)
	}
	for _, want := range []string{"prs", "jobs", "left", "reserved"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning %q does not mention %q", warnings[0], want)
		}
	}
}

// Every escape hatch is covered: without these a bad keymap is a trap.
func TestEveryEscapeHatchIsReserved(t *testing.T) {
	for _, key := range []string{"left", "right", "up", "down", "esc", "ctrl+c"} {
		if _, ok := Reserved(key); !ok {
			t.Errorf("%q is not reserved, so a keymap could claim it", key)
		}
	}
	// Named actions stay rebindable: reserving them would defeat the point
	// of a keymap.
	for _, key := range []string{"d", "c", "t", "j", "k", "/"} {
		if _, ok := Reserved(key); ok {
			t.Errorf("%q is reserved, but it should be rebindable", key)
		}
	}
}

// Case and spacing should not let a reserved key through.
func TestReservedIsNotCaseSensitive(t *testing.T) {
	for _, key := range []string{"ESC", " esc ", "Left", "CTRL+C"} {
		if _, ok := Reserved(key); !ok {
			t.Errorf("%q slipped past the reserved check", key)
		}
	}
}
