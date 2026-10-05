// Package herdr drives the Herdr terminal multiplexer through its CLI. It
// finds or creates the git worktree workspace an issue, pull request or agent
// session belongs to, and focuses it; agenda's herdr mode (`agenda --herdr`,
// run from a Herdr popup) is built on it.
package herdr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner runs name with args in dir ("" inherits the working directory) and
// returns its stdout. A failed command's error carries its stderr (see
// exec.ExitError), which is where herdr reports its JSON errors.
type Runner func(dir, name string, args ...string) ([]byte, error)

// ExecRunner runs commands for real.
func ExecRunner(dir, name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	return c.Output()
}

// Client calls the herdr CLI, which talks to the Herdr session this process
// runs in (herdr injects the socket path into every pane it manages).
type Client struct {
	bin string
	run Runner
}

// NewClient returns a client running herdr through run, preferring the binary
// herdr advertises in $HERDR_BIN_PATH.
func NewClient(run Runner) *Client {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return &Client{bin: bin, run: run}
}

// Workspace is a Herdr workspace. Worktree is set only for workspaces Herdr
// opened on a git worktree.
type Workspace struct {
	ID       string `json:"workspace_id"`
	Label    string `json:"label"`
	Focused  bool   `json:"focused"`
	Worktree *struct {
		CheckoutPath string `json:"checkout_path"`
		RepoRoot     string `json:"repo_root"`
	} `json:"worktree"`
}

// Agent is a coding agent Herdr recognized in a pane. Session identifies the
// conversation it runs (for Claude, the session id), when the agent reports it.
type Agent struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Session     *struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"agent_session"`
}

// Pane is a terminal pane.
type Pane struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Cwd         string `json:"cwd"`
	Focused     bool   `json:"focused"`
}

// Tab is a tab of a workspace.
type Tab struct {
	TabID string `json:"tab_id"`
	Label string `json:"label"`
}

// opened is what creating a workspace returns: it and its first tab and pane.
type opened struct {
	Workspace Workspace `json:"workspace"`
	Tab       Tab       `json:"tab"`
	RootPane  Pane      `json:"root_pane"`
}

func (c *Client) Workspaces() ([]Workspace, error) {
	var r struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	return r.Workspaces, c.call(&r, "workspace", "list")
}

// Tabs lists workspace's tabs.
func (c *Client) Tabs(workspace string) ([]Tab, error) {
	var r struct {
		Tabs []Tab `json:"tabs"`
	}
	return r.Tabs, c.call(&r, "tab", "list", "--workspace", workspace)
}

func (c *Client) Agents() ([]Agent, error) {
	var r struct {
		Agents []Agent `json:"agents"`
	}
	return r.Agents, c.call(&r, "agent", "list")
}

func (c *Client) Panes() ([]Pane, error) {
	var r struct {
		Panes []Pane `json:"panes"`
	}
	return r.Panes, c.call(&r, "pane", "list")
}

// CreateWorkspace opens a focused workspace whose first tab is on cwd.
func (c *Client) CreateWorkspace(cwd, label string) (opened, error) {
	var r opened
	return r, c.call(&r, "workspace", "create", "--cwd", cwd, "--label", label, "--focus")
}

func (c *Client) FocusWorkspace(id string) error {
	return c.call(nil, "workspace", "focus", id)
}

func (c *Client) FocusTab(id string) error {
	return c.call(nil, "tab", "focus", id)
}

func (c *Client) RenameTab(id, label string) error {
	return c.call(nil, "tab", "rename", id, label)
}

func (c *Client) FocusAgent(paneID string) error {
	return c.call(nil, "agent", "focus", paneID)
}

// CreateTab adds a focused tab on cwd to workspace and returns its pane.
func (c *Client) CreateTab(workspace, cwd, label string) (string, error) {
	var r struct {
		RootPane Pane `json:"root_pane"`
	}
	err := c.call(&r, "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--focus")
	return r.RootPane.PaneID, err
}

// Run types command into pane's shell and presses enter.
func (c *Client) Run(paneID, command string) error {
	return c.call(nil, "pane", "run", paneID, command)
}

// call runs a herdr subcommand and decodes the response's result into out
// (nil discards it). herdr answers {"result": ...} on stdout, or
// {"error": {"code", "message"}} on stderr with exit status 1.
func (c *Client) call(out any, args ...string) error {
	stdout, err := c.run("", c.bin, args...)
	if err != nil {
		return commandError("herdr "+args[0]+" "+args[1], err)
	}
	if out == nil {
		return nil
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		return fmt.Errorf("herdr %s %s: %w", args[0], args[1], err)
	}
	return json.Unmarshal(env.Result, out)
}

// commandError turns a failed command into an error naming what failed, using
// herdr's JSON error message or the command's stderr when there is one.
func commandError(what string, err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return fmt.Errorf("%s: %w", what, err)
	}
	stderr := bytes.TrimSpace(ee.Stderr)
	var herr struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(stderr, &herr) == nil && herr.Error.Message != "" {
		return fmt.Errorf("%s: %s", what, lastLine(herr.Error.Message))
	}
	if len(stderr) > 0 {
		return fmt.Errorf("%s: %s", what, lastLine(string(stderr)))
	}
	return fmt.Errorf("%s: %w", what, err)
}

// lastLine keeps the last line of a multi-line message: git and herdr put the
// actual failure ("fatal: ...") after their progress chatter.
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
