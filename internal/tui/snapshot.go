package tui

import (
	"github.com/sanity-labs/agenda/internal/cache"
)

const snapshotCache = "snapshot"

type filterSnapshot struct {
	Query         string   `json:"query"`
	Fields        []string `json:"fields"`
	CaseSensitive bool     `json:"case_sensitive"`
}

// snapshot is what `q` leaves behind with remember_on_quit on: the tab and
// each view's filter, keyed by tab title so a reordered or disabled view
// does not inherit another's.
type snapshot struct {
	View    string                    `json:"view"`
	Filters map[string]filterSnapshot `json:"filters"`
}

func (m Model) saveSnapshot() {
	s := snapshot{View: m.views[m.current].Title(), Filters: map[string]filterSnapshot{}}
	for _, v := range m.views {
		if f, ok := v.(filterable); ok {
			q, fields, cs := f.FilterState()
			s.Filters[v.Title()] = filterSnapshot{q, fields, cs}
		}
	}
	_ = cache.Save(snapshotCache, s)
}

// WithSnapshot restores what the last `q` saved. keepView is set when the
// tab was named on the command line, which wins over the snapshot.
func (m Model) WithSnapshot(keepView bool) Model {
	if !m.cfg.RememberOnQuit {
		return m
	}
	s, ok := cache.Load[snapshot](snapshotCache)
	if !ok {
		return m
	}
	for i, v := range m.views {
		if !keepView && v.Title() == s.View {
			m.current = i
		}
		if f, ok := v.(filterable); ok {
			if fs, ok := s.Filters[v.Title()]; ok {
				f.SetFilter(fs.Query, fs.Fields, fs.CaseSensitive)
			}
		}
	}
	return m
}
