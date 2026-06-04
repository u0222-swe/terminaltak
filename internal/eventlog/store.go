// Package eventlog is the rolling buffer of recent CoT events surfaced by
// the TUI's "l"-toggleable log overlay. It captures position / marker
// events only — GeoChat is handled by internal/chat and our own outbound
// PLI is filtered out by selfUID so the log is "things other clients sent".
package eventlog

import (
	"sync"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/safetext"
)

// Entry is one row of the log. Fields are denormalised so the TUI can
// render directly without re-parsing the original event.
type Entry struct {
	Time        time.Time
	UID         string
	Callsign    string
	Type        string
	Group       string
	Lat         float64
	Lon         float64
	Affiliation cot.Affiliation
}

// DefaultCapacity is the buffer size used when callers pass <= 0 to NewStore.
const DefaultCapacity = 1000

// Store is a thread-safe rolling buffer.
type Store struct {
	mu      sync.RWMutex
	buf     []Entry // append-only; oldest at index 0, newest at last
	cap     int
	selfUID string
}

// NewStore returns a Store with the given capacity. selfUID is filtered out
// of Apply so we don't log our own PLIs back to the user.
func NewStore(cap int, selfUID string) *Store {
	if cap <= 0 {
		cap = DefaultCapacity
	}
	return &Store{cap: cap, selfUID: selfUID}
}

// Apply records ev if it is a position-style event (CoT type "a-*-*").
// Chat events and our own UID are skipped.
func (s *Store) Apply(ev cot.Event) {
	if !ev.IsPosition() {
		return
	}
	if ev.UID == "" || ev.UID == s.selfUID {
		return
	}
	e := Entry{
		Time:        parseTime(ev.Time),
		UID:         ev.UID,
		Type:        safetext.Clean(ev.Type),
		Lat:         ev.Point.Lat,
		Lon:         ev.Point.Lon,
		Affiliation: ev.Affiliation(),
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if ev.Detail.Contact != nil {
		e.Callsign = safetext.Clean(ev.Detail.Contact.Callsign)
	}
	if ev.Detail.Group != nil {
		e.Group = safetext.Clean(ev.Detail.Group.Name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, e)
	if len(s.buf) > s.cap {
		// Drop oldest entries; copy to a fresh backing array to release
		// memory rather than holding onto a growing prefix slice.
		cp := make([]Entry, s.cap)
		copy(cp, s.buf[len(s.buf)-s.cap:])
		s.buf = cp
	}
}

// Recent returns up to limit entries newest-first. If filter is non-nil it
// is consulted per-entry against the entry's sender UID; entries for which
// it returns false are skipped.
func (s *Store) Recent(filter func(senderUID string) bool, limit int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = len(s.buf)
	}
	out := make([]Entry, 0, limit)
	for i := len(s.buf) - 1; i >= 0 && len(out) < limit; i-- {
		e := s.buf[i]
		if filter != nil && !filter(e.UID) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Len returns the total number of entries currently buffered, regardless of
// any filter.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.buf)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		cot.TimeFormat,
		"2006-01-02T15:04:05Z",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
