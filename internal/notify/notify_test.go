package notify

import (
	"testing"

	"github.com/sanity-labs/agenda/internal/ui"
)

// The herdr channel falls back to the in-app toast outside Herdr, so a
// config carried to a plain terminal still shows something.
func TestHerdrChannelFallsBackOutsideHerdr(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	n := New("herdr", "", "none")
	if n == nil {
		t.Fatal("herdr channel is not a notifier")
	}
	if _, ok := n.Notify("t", "b", "").(ui.ToastMsg); !ok {
		t.Error("outside Herdr the herdr channel did not fall back to a toast")
	}
}
