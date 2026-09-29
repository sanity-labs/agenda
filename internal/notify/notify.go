// Package notify posts notifications when something new shows up in a view
// (a PR waiting on your review, a newly-assigned Linear issue). Channels: an
// in-app terminal toast, or an OS desktop notification.
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/ui"
)

// Notifier posts one notification and returns the message the UI should
// process (a ui.ToastMsg for the terminal channel, nil otherwise). Calls may
// block (they shell out), so run them from a tea.Cmd, not the update loop.
// url is what clicking the notification opens, where the OS supports it.
type Notifier interface {
	Notify(title, body, url string) tea.Msg
}

// New returns a notifier for the configured popup channel ("terminal" or
// "desktop"), or nil when the channel is off; callers treat a nil Notifier
// as "notifications disabled". click is what a desktop notification does
// when clicked: "url" opens the item, "none" does nothing.
func New(popup string, sound bool, click string) Notifier {
	if popup != "terminal" && popup != "desktop" {
		return nil
	}
	return system{desktop: popup == "desktop", sound: sound, click: click}
}

type system struct {
	desktop bool
	sound   bool
	click   string
}

func (s system) Notify(title, body, url string) tea.Msg {
	if s.desktop {
		s.desktopNotify(title, body, url)
	}
	if s.sound {
		s.playSound()
	}
	if !s.desktop {
		return ui.ToastMsg{Title: title, Body: body}
	}
	return nil
}

// desktopNotify posts an OS notification. On macOS it prefers
// terminal-notifier, the only way to get an app icon and a clickable
// notification: osascript's notifications carry the Script Editor icon and
// do nothing when clicked. Without it, osascript still posts, just plainer.
func (s system) desktopNotify(title, body, url string) {
	switch runtime.GOOS {
	case "darwin":
		if bin, err := exec.LookPath("terminal-notifier"); err == nil {
			args := []string{"-title", "agenda", "-subtitle", title, "-message", body,
				"-group", "agenda"} // one group: a new notification replaces the last
			if url != "" && s.click == "url" {
				args = append(args, "-open", url)
			}
			// It exits 0 even when macOS has denied it permission, printing
			// the refusal to stderr, so the exit code alone would leave the
			// user with no notification and no clue.
			out, err := exec.Command(bin, args...).CombinedOutput()
			if err == nil && !strings.Contains(string(out), "Notifications are turned off") &&
				!strings.Contains(string(out), "not allowed") {
				return
			}
		}
		// osascript takes no click action, so the url is dropped here.
		script := fmt.Sprintf("display notification %q with title %q subtitle %q",
			body, "agenda", title)
		_ = exec.Command("osascript", "-e", script).Run()
	default:
		args := []string{"--app-name=agenda", title, body}
		if url != "" && s.click == "url" {
			// notify-send cannot open a URL itself; the body carries it so
			// the link is at least copyable from the notification.
			args[len(args)-1] = body + "\n" + url
		}
		_ = exec.Command("notify-send", args...).Run()
	}
}

func (s system) playSound() {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("afplay", "/System/Library/Sounds/Ping.aiff").Run()
	default:
		_ = exec.Command("canberra-gtk-play", "-i", "message").Run()
	}
}

// ClickHint explains why a click might do nothing, for the config overlay
// and the status line. Empty when clicking will work.
func ClickHint(click string) string {
	if click != "url" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("terminal-notifier"); err != nil {
			return "clicking needs terminal-notifier: brew install terminal-notifier"
		}
	case "linux":
		return "notify-send cannot open links, so the URL goes in the body"
	}
	return ""
}
