package eventlog

import (
	"testing"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

func mkPos(uid, callsign, group string, lat, lon float64, when time.Time) cot.Event {
	return cot.Event{
		Version: cot.Version,
		UID:     uid,
		Type:    "a-f-G-U-C",
		Time:    when.Format(cot.TimeFormat),
		Point:   cot.Point{Lat: lat, Lon: lon},
		Detail: cot.Detail{
			Contact: &cot.Contact{Callsign: callsign},
			Group:   &cot.Group{Name: group},
		},
	}
}

func TestApplyAppendsAndRecentReversesOrder(t *testing.T) {
	s := NewStore(10, "SELF")
	t0 := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		s.Apply(mkPos("UID-"+string(rune('A'+i)), "CALL-"+string(rune('A'+i)), "g", 0, 0, t0.Add(time.Duration(i)*time.Second)))
	}
	got := s.Recent(nil, 0)
	if len(got) != 5 {
		t.Fatalf("Recent len = %d", len(got))
	}
	if got[0].Callsign != "CALL-E" || got[4].Callsign != "CALL-A" {
		t.Errorf("not reversed: %+v", got)
	}
}

func TestApplyDropsOldestOverCapacity(t *testing.T) {
	s := NewStore(3, "SELF")
	t0 := time.Now().UTC()
	for i := 0; i < 5; i++ {
		s.Apply(mkPos("UID-"+string(rune('A'+i)), "C-"+string(rune('A'+i)), "g", 0, 0, t0.Add(time.Duration(i)*time.Second)))
	}
	if got := s.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}
	rec := s.Recent(nil, 0)
	// Newest first should be C-E, C-D, C-C; A and B dropped.
	if rec[0].Callsign != "C-E" || rec[2].Callsign != "C-C" {
		t.Errorf("got %v", entryCallsigns(rec))
	}
}

func TestApplySkipsSelfUID(t *testing.T) {
	s := NewStore(10, "SELF")
	s.Apply(mkPos("SELF", "ME", "g", 0, 0, time.Now()))
	if s.Len() != 0 {
		t.Errorf("self event leaked into log")
	}
}

func TestApplySkipsChat(t *testing.T) {
	s := NewStore(10, "SELF")
	chatEv, _ := cot.BuildGeoChat(
		cot.SelfInfo{UID: "X", Callsign: "X"},
		cot.AllChat{},
		"hi",
		"MSG-1",
		time.Now(),
	)
	s.Apply(chatEv)
	if s.Len() != 0 {
		t.Errorf("chat leaked into log")
	}
}

func TestRecentLimit(t *testing.T) {
	s := NewStore(100, "SELF")
	now := time.Now()
	for i := 0; i < 20; i++ {
		s.Apply(mkPos("U-"+string(rune('A'+i%26)), "C", "g", 0, 0, now.Add(time.Duration(i)*time.Second)))
	}
	if got := s.Recent(nil, 5); len(got) != 5 {
		t.Errorf("Recent(5) returned %d entries", len(got))
	}
}

func TestRecentFiltersBySenderUID(t *testing.T) {
	s := NewStore(100, "SELF")
	now := time.Now()
	s.Apply(mkPos("A", "ALICE", "Cyan", 0, 0, now))
	s.Apply(mkPos("B", "BOB", "Cyan", 0, 0, now.Add(time.Second)))
	s.Apply(mkPos("C", "CHARLIE", "Cyan", 0, 0, now.Add(2*time.Second)))

	got := s.Recent(func(uid string) bool { return uid == "A" || uid == "C" }, 0)
	if len(got) != 2 {
		t.Fatalf("Recent w/ filter len = %d, want 2", len(got))
	}
	for _, e := range got {
		if e.UID != "A" && e.UID != "C" {
			t.Errorf("filter leaked: %+v", e)
		}
	}
}

func TestApplyParsesEventFields(t *testing.T) {
	s := NewStore(10, "SELF")
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	s.Apply(mkPos("UID-1", "ALICE", "testchan_common", 59.33, 18.07, now))
	got := s.Recent(nil, 0)[0]
	if got.UID != "UID-1" {
		t.Errorf("UID = %q", got.UID)
	}
	if got.Lat != 59.33 || got.Lon != 18.07 {
		t.Errorf("lat/lon = %v/%v", got.Lat, got.Lon)
	}
	if got.Affiliation != cot.AffiliationFriendly {
		t.Errorf("Affiliation = %v", got.Affiliation)
	}
	if got.Time != now {
		t.Errorf("Time = %v, want %v", got.Time, now)
	}
}

func entryCallsigns(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Callsign
	}
	return out
}
