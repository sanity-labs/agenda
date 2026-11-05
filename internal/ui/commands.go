package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// CommandKind marks a search result that runs a palette command instead of
// jumping to an item; its ID names the command.
const CommandKind = "command"

// Command is something cmd+k can do to the selected item of a view.
type Command struct {
	ID, Title, Detail string
	Run               func() tea.Cmd
}

// Commander is a view that offers commands on its selected item. Commands
// is asked again on every keystroke, so it must be cheap and side-effect
// free; PrepareCommands runs once when the box opens, for whatever has to
// be fetched first (it answers with CommandsChangedMsg when it lands).
type Commander interface {
	Commands() []Command
	PrepareCommands() tea.Cmd
}

// CommandsChangedMsg asks an open box to ask its view for commands again.
type CommandsChangedMsg struct{}

// MatchCommands keeps the commands whose title contains every word typed.
func MatchCommands(cmds []Command, query string) []Command {
	words := strings.Fields(strings.ToLower(query))
	var out []Command
next:
	for _, c := range cmds {
		title := strings.ToLower(c.Title)
		for _, w := range words {
			if !strings.Contains(title, w) {
				continue next
			}
		}
		out = append(out, c)
	}
	return out
}
