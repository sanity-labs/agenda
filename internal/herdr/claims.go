package herdr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Node is something a workspace can claim: a Linear issue (whose sub-issues
// go with it), a Linear project (all its issues), or a PR with no issue.
type Node struct {
	Kind string `json:"kind"` // "issue" | "project" | "pr"
	Key  string `json:"key"`  // identifier, project id, or repo#N
}

// ClaimStore is where claims live: agenda's own file (Claims), or a Herdr
// workspace token another tool keeps (TokenClaims).
type ClaimStore interface {
	// Owner returns the live workspace claiming n, or "".
	Owner(n Node, live []Workspace) string
	// Add records that workspace ws (labelled label) claims n.
	Add(ws, label string, n Node) error
}

// TokenClaims reads claims from the workspace token Name, which another tool
// keeps (herdr.claims: tokens). Its value lists what the workspace claims,
// separated by spaces or commas: an issue identifier (SRE-5170), a PR
// (agenda#28 or sanity-labs/agenda#28), or kind:key (issue:SRE-5170,
// project:<Linear project id>, pr:agenda#28). agenda never writes the
// token: tokens are one map per workspace that any tool can overwrite, so
// the tool that keeps them is the only writer.
type TokenClaims struct{ Name string }

func (t TokenClaims) Owner(n Node, live []Workspace) string {
	for _, w := range live {
		for _, e := range strings.FieldsFunc(w.Tokens[t.Name], func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			if tokenNode(e) == n {
				return w.ID
			}
		}
	}
	return ""
}

// Add records nothing: the token's owner decides what a workspace claims.
func (TokenClaims) Add(string, string, Node) error { return nil }

// tokenNode reads one entry of a claims token as the node it names.
func tokenNode(e string) Node {
	kind, key, ok := strings.Cut(e, ":")
	if !ok || (kind != "issue" && kind != "project" && kind != "pr") {
		kind, key = "issue", e
		if strings.Contains(e, "#") {
			kind = "pr"
		}
	}
	switch kind {
	case "issue":
		key = strings.ToUpper(key)
	case "pr":
		// prKey names a PR by repository name alone.
		key = key[strings.LastIndexByte(key, '/')+1:]
	}
	return Node{kind, key}
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
