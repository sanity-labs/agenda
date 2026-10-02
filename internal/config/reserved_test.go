package config

import (
	"strings"
	"testing"
)

// A reserved key cannot be pointed at a different action: that is what
// would leave someone unable to get out of a state without editing the
// config by hand.
func TestReservedKeysCannotBePointedElsewhere(t *testing.T) {
	k := Keymap{"prs": {"jobs": Chord{"left"}, "diff": Chord{"D", "esc"}}}

	if got := k.Of("prs", "jobs", "t"); len(got) != 0 {
		t.Errorf("left bound to prs.jobs resolved to %v, want nothing", got)
	}
	// The rest of a partly-reserved binding still works: dropping the whole
	// override would lose a key the user legitimately asked for.
	if got := k.Of("prs", "diff", "d"); len(got) != 1 || got[0] != "D" {
		t.Errorf("binding = %v, want just the non-reserved key", got)
	}
}

// But the key keeps working on the action it already means, so writing
// out a default does not silently lose it.
func TestReservedKeysSurviveOnTheirOwnAction(t *testing.T) {
	k := Keymap{"list": {
		"up":   Chord{"up", "k"},
		"down": Chord{"down", "j"},
	}}
	for _, c := range []struct {
		action string
		want   []string
	}{
		{"up", []string{"up", "k"}},
		{"down", []string{"down", "j"}},
	} {
		got := k.Of("list", c.action, c.want...)
		if len(got) != len(c.want) {
			t.Errorf("list.%s = %v, want %v", c.action, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("list.%s = %v, want %v", c.action, got, c.want)
				break
			}
		}
	}
}

// Binding more keys to the same actions is the point: hjkl alongside the
// arrows, or pgup/pgdown for paging.
func TestExtraKeysForReservedActions(t *testing.T) {
	k := Keymap{"list": {
		"up":        Chord{"up", "k", "ctrl+p"},
		"half_down": Chord{"pgdown", "ctrl+d"},
	}}
	if got := k.Of("list", "up", "up"); len(got) != 3 {
		t.Errorf("list.up = %v, want all three kept", got)
	}
	if got := k.Of("list", "half_down", "ctrl+d"); len(got) != 2 {
		t.Errorf("list.half_down = %v, want both kept", got)
	}
}

// And the user is told when one is refused, or the key keeps its built-in
// meaning and the override looks like it simply did not work.
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

	// A default written out is not a warning: nothing was refused.
	ok := Keymap{"list": {"up": Chord{"up", "k"}}}
	if w := ok.reservedWarnings(); len(w) != 0 {
		t.Errorf("warned about a key on its own action: %v", w)
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
