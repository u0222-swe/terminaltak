// Package markers is the in-memory store of map markers the local user has
// placed with the point dropper. It is deliberately separate from
// internal/contacts (live tracks of other clients): markers are owned by this
// client, rendered immediately (even while offline), and removable. The TUI
// renders the snapshot on the world map and lists it in the markers overlay.
package markers

import (
	"sort"
	"sync"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

// Marker is a single user-placed point.
type Marker struct {
	UID         string
	Label       string
	Remarks     string
	Type        string // CoT type, e.g. "a-h-G"
	Affiliation cot.Affiliation
	Lat, Lon    float64
	HAE         float64
	Created     time.Time
}

// Store is a thread-safe set of markers keyed by UID.
type Store struct {
	mu      sync.RWMutex
	markers map[string]Marker
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{markers: make(map[string]Marker)}
}

// Add inserts or replaces a marker by UID.
func (s *Store) Add(m Marker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markers[m.UID] = m
}

// Remove deletes the marker with the given UID, if present.
func (s *Store) Remove(uid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.markers, uid)
}

// Get returns the marker with the given UID.
func (s *Store) Get(uid string) (Marker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.markers[uid]
	return m, ok
}

// Has reports whether a marker with the given UID exists.
func (s *Store) Has(uid string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.markers[uid]
	return ok
}

// Snapshot returns all markers, newest first.
func (s *Store) Snapshot() []Marker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Marker, 0, len(s.markers))
	for _, m := range s.markers {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created.Equal(out[j].Created) {
			return out[i].UID < out[j].UID
		}
		return out[i].Created.After(out[j].Created)
	})
	return out
}

// Len returns the number of stored markers.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.markers)
}
