package markers

import (
	"testing"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

func TestStoreAddGetRemove(t *testing.T) {
	s := NewStore()
	if s.Len() != 0 {
		t.Fatalf("new store len = %d", s.Len())
	}
	m := Marker{UID: "a", Label: "Tgt", Type: "a-h-G", Affiliation: cot.AffiliationHostile, Lat: 1, Lon: 2}
	s.Add(m)
	if !s.Has("a") || s.Len() != 1 {
		t.Fatalf("after add: has=%v len=%d", s.Has("a"), s.Len())
	}
	got, ok := s.Get("a")
	if !ok || got.Label != "Tgt" {
		t.Fatalf("get = %+v ok=%v", got, ok)
	}
	s.Remove("a")
	if s.Has("a") || s.Len() != 0 {
		t.Fatalf("after remove: has=%v len=%d", s.Has("a"), s.Len())
	}
}

func TestStoreSnapshotNewestFirst(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	s.Add(Marker{UID: "old", Created: base})
	s.Add(Marker{UID: "new", Created: base.Add(time.Minute)})
	snap := s.Snapshot()
	if len(snap) != 2 || snap[0].UID != "new" || snap[1].UID != "old" {
		t.Fatalf("snapshot order = %+v", snap)
	}
}
