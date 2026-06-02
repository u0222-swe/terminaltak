package contacts

import (
	"testing"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

func mkEvent(uid, callsign, teamColor, role string, lat, lon float64, timeStr, staleStr string) cot.Event {
	ev := cot.Event{
		Version: cot.Version,
		UID:     uid,
		Type:    "a-f-G-U-C",
		Time:    timeStr,
		Stale:   staleStr,
		Point:   cot.Point{Lat: lat, Lon: lon},
		Detail:  cot.Detail{Contact: &cot.Contact{Callsign: callsign}},
	}
	if teamColor != "" || role != "" {
		ev.Detail.Group = &cot.Group{Name: teamColor, Role: role}
	}
	return ev
}

func TestApplyAddsAndUpdates(t *testing.T) {
	s := NewStore("SELF")
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC).Format(cot.TimeFormat)
	stale := time.Date(2026, 4, 29, 10, 1, 0, 0, time.UTC).Format(cot.TimeFormat)
	s.Apply(mkEvent("UID-1", "ALICE", "Cyan", "HQ", 59.33, 18.07, now, stale))

	got := s.Snapshot(nil)
	if len(got) != 1 {
		t.Fatalf("Snapshot len = %d", len(got))
	}
	if got[0].Callsign != "ALICE" || got[0].TeamColor != "Cyan" || got[0].Role != "HQ" {
		t.Errorf("contact = %+v", got[0])
	}
	if got[0].Lat != 59.33 || got[0].Lon != 18.07 {
		t.Errorf("lat/lon = %v/%v", got[0].Lat, got[0].Lon)
	}

	now2 := time.Date(2026, 4, 29, 10, 5, 0, 0, time.UTC).Format(cot.TimeFormat)
	s.Apply(mkEvent("UID-1", "ALICE", "Cyan", "HQ", 60.0, 19.0, now2, stale))
	got = s.Snapshot(nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 contact after update, got %d", len(got))
	}
	if got[0].Lat != 60.0 || got[0].Lon != 19.0 {
		t.Errorf("position not updated: %+v", got[0])
	}
}

func TestApplySkipsSelfUID(t *testing.T) {
	s := NewStore("SELF")
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC).Format(cot.TimeFormat)
	s.Apply(mkEvent("SELF", "ME", "Cyan", "HQ", 0, 0, now, ""))
	if got := s.Snapshot(nil); len(got) != 0 {
		t.Errorf("expected self event filtered out, got %d contacts", len(got))
	}
}

func TestSnapshotFilterOnUID(t *testing.T) {
	s := NewStore("SELF")
	now := time.Now().UTC().Format(cot.TimeFormat)
	stale := time.Now().Add(time.Minute).UTC().Format(cot.TimeFormat)
	s.Apply(mkEvent("A", "ALICE", "Cyan", "HQ", 0, 0, now, stale))
	s.Apply(mkEvent("B", "BOB", "Yellow", "FOX", 0, 0, now, stale))
	only := s.Snapshot(func(c Contact) bool { return c.UID == "A" })
	if len(only) != 1 || only[0].Callsign != "ALICE" {
		t.Errorf("filtered snapshot = %+v", only)
	}
}

func TestGetByUID(t *testing.T) {
	s := NewStore("SELF")
	now := time.Now().UTC().Format(cot.TimeFormat)
	stale := time.Now().Add(time.Minute).UTC().Format(cot.TimeFormat)
	s.Apply(mkEvent("UID-1", "ALICE", "Cyan", "HQ", 59.33, 18.07, now, stale))
	c, ok := s.Get("UID-1")
	if !ok {
		t.Fatal("Get returned !ok")
	}
	if c.Callsign != "ALICE" {
		t.Errorf("Get callsign = %q", c.Callsign)
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("Get on unknown UID returned ok")
	}
}

func TestBoundingBox(t *testing.T) {
	s := NewStore("SELF")
	now := time.Now().UTC().Format(cot.TimeFormat)
	stale := time.Now().Add(time.Minute).UTC().Format(cot.TimeFormat)
	s.Apply(mkEvent("A", "A", "Cyan", "", 59.33, 18.07, now, stale))
	s.Apply(mkEvent("B", "B", "Cyan", "", 60.0, 19.0, now, stale))
	s.Apply(mkEvent("C", "C", "Cyan", "", 58.5, 17.5, now, stale))

	minLat, maxLat, minLon, maxLon, ok := s.BoundingBox(nil)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if minLat != 58.5 || maxLat != 60.0 {
		t.Errorf("lat range = [%v, %v]", minLat, maxLat)
	}
	if minLon != 17.5 || maxLon != 19.0 {
		t.Errorf("lon range = [%v, %v]", minLon, maxLon)
	}
}

func TestBoundingBoxEmpty(t *testing.T) {
	s := NewStore("SELF")
	if _, _, _, _, ok := s.BoundingBox(nil); ok {
		t.Error("expected ok=false for empty store")
	}
}

func TestPruneStale(t *testing.T) {
	s := NewStore("SELF")
	now := time.Now().UTC()
	soonStale := now.Add(-1 * time.Second).Format(cot.TimeFormat)
	farStale := now.Add(time.Hour).Format(cot.TimeFormat)
	s.Apply(mkEvent("OLD", "X", "Cyan", "", 0, 0, now.Format(cot.TimeFormat), soonStale))
	s.Apply(mkEvent("NEW", "Y", "Cyan", "", 0, 0, now.Format(cot.TimeFormat), farStale))
	removed := s.PruneStale(now)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	got := s.Snapshot(nil)
	if len(got) != 1 || got[0].UID != "NEW" {
		t.Errorf("after prune: %+v", got)
	}
}

func TestApplyIgnoresNonAtomEvents(t *testing.T) {
	s := NewStore("SELF")
	chat := cot.Event{
		Version: cot.Version,
		UID:     "GeoChat.x.y.z",
		Type:    "b-t-f",
		Detail:  cot.Detail{Chat: &cot.Chat{Chatroom: "All Chat Rooms"}},
	}
	s.Apply(chat)
	if got := s.Snapshot(nil); len(got) != 0 {
		t.Errorf("chat event leaked into contacts: %+v", got)
	}
}
