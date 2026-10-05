package herdr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Node is something a workspace can claim: a Linear issue (whose sub-issues
// go with it), a Linear project (all its issues), or a PR with no issue.
type Node struct {
	Kind string `json:"kind"` // "issue" | "project" | "pr"
	Key  string `json:"key"`  // identifier, project id, or repo#N
}

type claim struct {
	Workspace string `json:"workspace"`
	Label     string `json:"label"` // when last seen, without Herdr's [n] prefix
	Nodes     []Node `json:"nodes"`
}

// Claims records which workspace holds which nodes. Herdr keeps a
// workspace's id and label across restarts but drops tool metadata, so agenda
// keeps this itself. A claimed workspace whose id is gone is found again by
// its label.
type Claims struct {
	path   string
	claims []claim
}

// DefaultClaimsPath is $XDG_STATE_HOME/agenda/herdr-claims.json, or
// ~/.local/state/agenda/herdr-claims.json.
func DefaultClaimsPath() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(d, "agenda", "herdr-claims.json")
}

// LoadClaims reads the claims at path. A missing or unreadable file starts
// empty: the cost is being asked again, and labels still match.
func LoadClaims(path string) *Claims {
	c := &Claims{path: path}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &c.claims)
	}
	return c
}

// Owner returns the live workspace claiming n, or "".
func (c *Claims) Owner(n Node, live []Workspace) string {
	for i := range c.claims {
		cl := &c.claims[i]
		if !slices.Contains(cl.Nodes, n) {
			continue
		}
		for _, w := range live {
			if w.ID == cl.Workspace {
				cl.Label = baseLabel(w.Label) // follow a rename
				return w.ID
			}
		}
		for _, w := range live {
			if baseLabel(w.Label) == cl.Label {
				cl.Workspace = w.ID // same workspace under a new id
				return w.ID
			}
		}
	}
	return ""
}

// Add records that workspace ws (labelled label) claims n, moving n from any
// workspace that claimed it before, and saves.
func (c *Claims) Add(ws, label string, n Node) error {
	var kept []claim
	found := false
	for _, cl := range c.claims {
		cl.Nodes = slices.DeleteFunc(cl.Nodes, func(x Node) bool { return x == n })
		if cl.Workspace == ws {
			cl.Label, cl.Nodes, found = baseLabel(label), append(cl.Nodes, n), true
		}
		if len(cl.Nodes) > 0 {
			kept = append(kept, cl)
		}
	}
	if !found {
		kept = append(kept, claim{Workspace: ws, Label: baseLabel(label), Nodes: []Node{n}})
	}
	c.claims = kept
	return c.save()
}

// save writes the claims atomically (temp file and rename), so a crash or a
// second instance can't leave half a file.
func (c *Claims) save() error {
	if c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c.claims, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".herdr-claims-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}

// labelNumber is the "[3] " Herdr puts in front of a numbered workspace's
// label, which comes and goes as workspaces are renumbered.
var labelNumber = regexp.MustCompile(`^\[\d+\]\s*`)

func baseLabel(l string) string {
	return strings.TrimSpace(labelNumber.ReplaceAllString(l, ""))
}
