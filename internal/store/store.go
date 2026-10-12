// Package store is a shared, in-memory metadata cache. Each view publishes the
// facts it owns — the PRs view publishes pull-request status, the sessions view
// publishes which issues/PRs each session mentions — and any view can read them
// to enrich its own display without depending on the other view's package.
//
// This is what lets the Linear view show CI/review icons for a referenced PR
// (data the PR view has) and list the agent sessions that mention an issue
// (data the sessions view has), and vice-versa.
//
// All access is single-threaded in practice (writes happen in Bubble Tea
// Update, reads in View, both on the main loop), but the methods take a lock so
// the store is safe to touch from a command goroutine too.
package store

import (
	"strings"
	"sync"
	"time"
)

// --- pull requests ----------------------------------------------------------

// PRState is a pull request's lifecycle state.
type PRState string

const (
	PROpen   PRState = "open"
	PRDraft  PRState = "draft"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed"
)

// CIState is the rolled-up CI/check status of a PR's head commit.
type CIState string

const (
	CIUnknown CIState = ""
	CIPending CIState = "pending"
	CIPassing CIState = "passing"
	CIFailing CIState = "failing"
)

// ReviewState is the rolled-up review decision of a PR.
type ReviewState string

const (
	ReviewNone     ReviewState = ""
	ReviewPending  ReviewState = "pending"
	ReviewApproved ReviewState = "approved"
	ReviewChanges  ReviewState = "changes_requested"
)

// PR is the cross-view metadata for a GitHub pull request, keyed by URL.
type PR struct {
	URL          string
	Repo         string
	Number       int
	Title        string
	State        PRState
	CI           CIState
	Review       ReviewState
	HasConflicts bool
	UpdatedAt    time.Time
}

// --- linear issues ----------------------------------------------------------

// Issue is the cross-view metadata for a Linear issue, keyed by identifier.
type Issue struct {
	Identifier string
	Title      string
	State      string
	StateType  string // backlog, unstarted, started, completed, canceled, triage
	StateColor string // Linear's hex for the state
	URL        string
}

// --- session mentions -------------------------------------------------------

// SessionRef identifies an agent session that mentions an entity, with a short
// snippet of surrounding context for display.
type SessionRef struct {
	Path    string // session file path (the session view's identity)
	Tool    string // "claude" | "codex" | "agy"
	Title   string
	Cwd     string
	Snippet string // a line of context where the mention occurred
}

// Key builds a reverse-index key for a mentioned entity. Kind is "linear" or
// "pr"; id is the issue identifier (case-folded) or the PR URL.
func Key(kind, id string) string {
	if kind == "linear" {
		id = strings.ToUpper(id)
	}
	return kind + ":" + id
}

// --- store ------------------------------------------------------------------

type Store struct {
	mu     sync.RWMutex
	prs    map[string]PR
	issues map[string]Issue
	// teams is every team key an issue has been seen from; missing is
	// every identifier a lookup said does not exist. Together they say
	// whether an ABC-123 in some text is a ticket or just a word.
	teams           map[string]bool
	missing         map[string]bool
	sessionMentions map[string][]SessionRef
}

func New() *Store {
	return &Store{
		prs:             map[string]PR{},
		issues:          map[string]Issue{},
		sessionMentions: map[string][]SessionRef{},
		teams:           map[string]bool{},
		missing:         map[string]bool{},
	}
}

// PutPRs upserts pull-request records (keyed by URL). Records are never removed,
// so a PR that has since left the publisher's view keeps its last-known status.
func (s *Store) PutPRs(prs []PR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range prs {
		s.prs[p.URL] = p
	}
}

// PR returns the stored metadata for a PR URL.
func (s *Store) PR(url string) (PR, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.prs[url]
	return p, ok
}

// PutIssues upserts Linear issue records, keyed by identifier (upper-cased).
func (s *Store) PutIssues(issues []Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, i := range issues {
		id := strings.ToUpper(i.Identifier)
		s.issues[id] = i
		delete(s.missing, id)
		if key, _, ok := strings.Cut(id, "-"); ok {
			s.teams[key] = true
		}
	}
}

// MarkMissing records that a lookup found no issue with this identifier,
// so it stops being shown as one.
func (s *Store) MarkMissing(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.missing[strings.ToUpper(id)] = true
}

// Vouched reports whether an identifier should be shown as a Linear issue:
// one that is loaded, or one from a team issues have been seen from that
// no lookup has refused yet. With no team seen yet nothing is refused, so
// the first paint before Linear loads still shows what it finds.
func (s *Store) Vouched(id string) bool {
	id = strings.ToUpper(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.issues[id]; ok {
		return true
	}
	if s.missing[id] {
		return false
	}
	if len(s.teams) == 0 {
		return true
	}
	key, _, _ := strings.Cut(id, "-")
	return s.teams[key]
}

// Issue returns the stored metadata for an issue identifier (case-insensitive).
func (s *Store) Issue(id string) (Issue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.issues[strings.ToUpper(id)]
	return i, ok
}

// SetSessionMentions replaces the whole session-mention index. The sessions
// view owns this index and republishes the full set on each scan.
func (s *Store) SetSessionMentions(index map[string][]SessionRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionMentions = index
}

// SessionsMentioning returns the sessions that mention the entity with the
// given key (see Key), in publication order.
func (s *Store) SessionsMentioning(key string) []SessionRef {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionMentions[key]
}
