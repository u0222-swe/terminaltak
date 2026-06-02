// Package contacts maintains the in-memory situation picture: every contact
// last seen on the wire. The store is intentionally passive — it does not
// own any toggle / display-filter state. The TUI layers a channel-based
// filter on top via the Snapshot predicate (sender UID -> allowed?), so the
// store stays a single source of truth for "what's currently in scope".
package contacts

import (
	"sync"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

// Contact is the snapshot of a single TAK contact.
type Contact struct {
	UID         string
	Callsign    string
	Type        string
	TeamColor   string // ATAK display attribute from <__group name="..."/>
	Role        string
	Affiliation cot.Affiliation
	Domain      cot.Domain
	Lat         float64
	Lon         float64
	HAE         float64
	LastSeen    time.Time
	Stale       time.Time
}

// Store is the central contact state.
type Store struct {
	mu       sync.RWMutex
	selfUID  string
	contacts map[string]Contact
}

// NewStore returns an empty Store. selfUID is filtered out of inbound events
// so we don't render our own track in the contacts list.
func NewStore(selfUID string) *Store {
	return &Store{
		selfUID:  selfUID,
		contacts: make(map[string]Contact),
	}
}

// Apply updates the store with a single CoT event. Only "atom" events
// (positions, type "a-*-*") create or update contacts; other types are
// ignored here (chat is handled in internal/chat, log in internal/eventlog).
func (s *Store) Apply(ev cot.Event) {
	if !ev.IsPosition() {
		return
	}
	if ev.UID == "" || ev.UID == s.selfUID {
		return
	}
	now := parseTime(ev.Time)
	stale := parseTime(ev.Stale)

	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.contacts[ev.UID]
	c.UID = ev.UID
	c.Type = ev.Type
	c.Affiliation = ev.Affiliation()
	c.Domain = ev.Domain()
	c.Lat = ev.Point.Lat
	c.Lon = ev.Point.Lon
	c.HAE = ev.Point.HAE
	if !now.IsZero() {
		c.LastSeen = now
	} else {
		c.LastSeen = time.Now().UTC()
	}
	if !stale.IsZero() {
		c.Stale = stale
	}
	if ev.Detail.Contact != nil && ev.Detail.Contact.Callsign != "" {
		c.Callsign = ev.Detail.Contact.Callsign
	}
	if ev.Detail.Group != nil {
		c.TeamColor = ev.Detail.Group.Name
		c.Role = ev.Detail.Group.Role
	}

	s.contacts[ev.UID] = c
}

// Snapshot returns every contact that passes filter. If filter is nil, all
// contacts are returned. The filter receives the full Contact so callers
// can match on UID, team-colour, callsign, etc.
func (s *Store) Snapshot(filter func(Contact) bool) []Contact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Contact, 0, len(s.contacts))
	for _, c := range s.contacts {
		if filter != nil && !filter(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Get returns a copy of the contact with the given UID, if any.
func (s *Store) Get(uid string) (Contact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.contacts[uid]
	return c, ok
}

// BoundingBox returns the lat/lon extent of all contacts that pass filter
// (filter may be nil to include every contact). The fifth return value is
// false when no contacts qualify.
func (s *Store) BoundingBox(filter func(Contact) bool) (minLat, maxLat, minLon, maxLon float64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	first := true
	for _, c := range s.contacts {
		if filter != nil && !filter(c) {
			continue
		}
		if first {
			minLat, maxLat = c.Lat, c.Lat
			minLon, maxLon = c.Lon, c.Lon
			first = false
			continue
		}
		if c.Lat < minLat {
			minLat = c.Lat
		}
		if c.Lat > maxLat {
			maxLat = c.Lat
		}
		if c.Lon < minLon {
			minLon = c.Lon
		}
		if c.Lon > maxLon {
			maxLon = c.Lon
		}
	}
	return minLat, maxLat, minLon, maxLon, !first
}

// PruneStale removes every contact whose Stale time is at or before now.
// Returns the number of contacts removed.
func (s *Store) PruneStale(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for uid, c := range s.contacts {
		if !c.Stale.IsZero() && !c.Stale.After(now) {
			delete(s.contacts, uid)
			removed++
		}
	}
	return removed
}

// Clear removes every contact. Intended for places where an external
// state change (e.g. channel toggle) makes the cached snapshot
// untrustworthy: rather than waiting for the per-contact stale window,
// drop the lot and let the active subscriptions re-populate from their
// next PLI. Returns the number of contacts removed.
func (s *Store) Clear() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.contacts)
	s.contacts = make(map[string]Contact)
	return n
}

// parseTime tries the standard CoT timestamp layout first, falling back to
// a few common variants we have observed.
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
