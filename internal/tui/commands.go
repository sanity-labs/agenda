package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sanity-labs/agenda/internal/config"
	"github.com/sanity-labs/agenda/internal/ui"
)

// commandFielder is a view whose selected row can feed a user command:
// scope is its keymap scope, fields are nil when nothing is selected.
type commandFielder interface {
	CommandFields() (scope string, fields map[string]string)
}

// pendingCommand is a command waiting on its prompt's answer.
type pendingCommand struct {
	cmd    config.Command
	env    []string
	picker ui.Picker
}

// validCommands keeps the commands that can fire, and says why each other
// one was dropped: a key that is reserved or already bound would be dead.
func validCommands(cfg config.Config) ([]config.Command, []string) {
	var ok []config.Command
	var warn []string
	for i, c := range cfg.Commands {
		name := fmt.Sprintf("commands[%d] (%s)", i, c.Key)
		target := keyEntry{scope: c.View, action: "command"}
		switch {
		case c.Key == "" || c.Run == "":
			warn = append(warn, name+": needs key and run")
		case c.View != "prs" && c.View != "linear" && c.View != "sessions":
			warn = append(warn, name+": view must be prs, linear or sessions")
		case c.Prompt != nil && len(c.Prompt.Options) == 0:
			warn = append(warn, name+": prompt needs options")
		default:
			if why, reserved := config.Reserved(c.Key); reserved {
				warn = append(warn, name+": key is reserved ("+why+")")
			} else if e, taken := findCollision(cfg.Keys, target, c.Key); taken {
				warn = append(warn, name+": key is bound to "+e.scope+"."+e.action)
			} else if dup := commandFor(ok, c.View, c.Key); dup != nil {
				warn = append(warn, name+": key is bound to another command")
			} else {
				ok = append(ok, c)
			}
		}
	}
	return ok, warn
}

func commandFor(cmds []config.Command, scope, key string) *config.Command {
	for i := range cmds {
		if cmds[i].View == scope && cmds[i].Key == key {
			return &cmds[i]
		}
	}
	return nil
}

// startCommand runs the command bound to key in the current view, or opens
// its prompt. Reports false when no command has the key.
func (m *Model) startCommand(key string) (tea.Cmd, bool) {
	f, ok := m.views[m.current].(commandFielder)
	if !ok {
		return nil, false
	}
	scope, fields := f.CommandFields()
	c := commandFor(m.commands, scope, key)
	if c == nil || fields == nil {
		return nil, false
	}
	env := make([]string, 0, len(fields)+1)
	for k, v := range fields {
		env = append(env, "AGENDA_"+strings.ToUpper(k)+"="+v)
	}
	if c.Prompt == nil {
		return runCommand(*c, env), true
	}
	items := make([]ui.PickerItem, len(c.Prompt.Options))
	for i, o := range c.Prompt.Options {
		items[i] = ui.PickerItem{Label: o}
	}
	title := c.Prompt.Title
	if title == "" {
		title = c.Description
	}
	m.pending = &pendingCommand{cmd: *c, env: env, picker: ui.NewPicker(title, items)}
	return nil, true
}

// updatePending routes a key to the open prompt.
func (m *Model) updatePending(msg tea.KeyMsg) tea.Cmd {
	p := m.pending
	switch p.picker.Update(msg) {
	case ui.PickerCancel:
		m.pending = nil
	case ui.PickerConfirm:
		m.pending = nil
		choice := p.cmd.Prompt.Options[p.picker.Index()]
		return runCommand(p.cmd, append(p.env, "AGENDA_CHOICE="+choice))
	}
	return nil
}

func runCommand(c config.Command, env []string) tea.Cmd {
	cmd := exec.Command("sh", "-c", c.Run)
	cmd.Env = append(os.Environ(), env...)
	name := c.Description
	if name == "" {
		name = c.Run
	}
	if c.Mode == "foreground" {
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return ui.ToastMsg{Title: name + " failed", Body: err.Error()}
			}
			return nil
		})
	}
	return func() tea.Msg {
		out, err := cmd.CombinedOutput()
		last := lastLine(string(out))
		if err != nil {
			if last == "" {
				last = err.Error()
			}
			return ui.ToastMsg{Title: name + " failed", Body: last}
		}
		return ui.ToastMsg{Title: name, Body: last, Success: true}
	}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndex(s, "\n")+1:]
}
