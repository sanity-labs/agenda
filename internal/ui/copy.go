package ui

import (
	"errors"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// CopyCmd puts text on the clipboard and says so: a toast naming what was
// copied and showing the text, so the key is seen to have done something.
// pbcopy on macOS, wl-copy or xclip on Linux, whichever is installed.
func CopyCmd(text, what string) tea.Cmd {
	if text == "" {
		return nil
	}
	return func() tea.Msg {
		if err := copyToClipboard(text); err != nil {
			return ToastMsg{Title: "Copy failed", Body: err.Error()}
		}
		return ToastMsg{Title: "Copied " + what, Body: text, Success: true}
	}
}

func copyToClipboard(text string) error {
	for _, argv := range [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}} {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdin = strings.NewReader(text)
		return c.Run()
	}
	return errors.New("no clipboard tool found (pbcopy, wl-copy or xclip)")
}
