package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/update"
)

// updateAvailableMsg carries the newer version found by the startup check.
type updateAvailableMsg string

func checkUpdate(current string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), update.Timeout)
		defer cancel()
		res := update.Check(ctx, current)
		if !res.Available {
			return nil
		}
		return updateAvailableMsg(res.Latest.Version)
	}
}
