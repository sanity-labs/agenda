package notify

import (
	"runtime"
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The terminal channel still returns a toast, and the URL does not leak
// into it: an in-app toast has nothing to click.
func TestTerminalChannelUnaffectedByURL(t *testing.T) {
	msg := system{click: "url"}.Notify("title", "body", "https://example.com")
	toast, ok := msg.(ui.ToastMsg)
	if !ok {
		t.Fatalf("got %T, want a ToastMsg", msg)
	}
	if strings.Contains(toast.Body, "example.com") {
		t.Errorf("the URL leaked into the toast body: %q", toast.Body)
	}
}

// The hint exists to explain a click doing nothing; it must stay quiet when
// clicking is off.
func TestClickHintSilentWhenDisabled(t *testing.T) {
	if got := ClickHint("none"); got != "" {
		t.Errorf("ClickHint(none) = %q, want empty", got)
	}
}

func TestClickHintNamesTheFix(t *testing.T) {
	got := ClickHint("url")
	switch runtime.GOOS {
	case "darwin":
		// Either terminal-notifier is installed (no hint) or the hint says
		// how to install it.
		if got != "" && !strings.Contains(got, "terminal-notifier") {
			t.Errorf("hint = %q, want it to name terminal-notifier", got)
		}
	case "linux":
		if !strings.Contains(got, "notify-send") {
			t.Errorf("hint = %q, want it to explain notify-send", got)
		}
	}
}
