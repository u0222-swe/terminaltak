package chat

import (
	"testing"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

func TestIngestAllChat(t *testing.T) {
	s := NewStore("ME")
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	ev, err := cot.BuildGeoChat(
		cot.SelfInfo{UID: "ALICE-UID", Callsign: "ALICE"},
		cot.AllChat{},
		"hello world",
		"MSG-1",
		now,
	)
	if err != nil {
		t.Fatalf("BuildGeoChat: %v", err)
	}
	m := s.IngestEvent(ev)
	if m == nil {
		t.Fatal("IngestEvent returned nil")
	}
	if m.Text != "hello world" {
		t.Errorf("text = %q", m.Text)
	}
	if m.SenderCallsign != "ALICE" {
		t.Errorf("sender callsign = %q", m.SenderCallsign)
	}
	if m.Direction != DirectionIn {
		t.Errorf("direction = %v", m.Direction)
	}

	got := s.AllChat(nil)
	if len(got) != 1 || got[0].ID != m.ID {
		t.Errorf("AllChat = %+v", got)
	}
}

func TestIngestNonChatReturnsNil(t *testing.T) {
	s := NewStore("ME")
	pli := cot.BuildPLI(cot.SelfInfo{UID: "X"}, time.Minute, time.Now())
	if m := s.IngestEvent(pli); m != nil {
		t.Errorf("expected nil for PLI, got %+v", m)
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d", s.Len())
	}
}

func TestIngestDM(t *testing.T) {
	s := NewStore("ME")
	now := time.Now()
	ev, err := cot.BuildGeoChat(
		cot.SelfInfo{UID: "ALICE-UID", Callsign: "ALICE"},
		cot.DM{RecipientUID: "ME", RecipientCallsign: "BOB"},
		"hi bob",
		"MSG-DM",
		now,
	)
	if err != nil {
		t.Fatalf("BuildGeoChat: %v", err)
	}
	m := s.IngestEvent(ev)
	if m == nil {
		t.Fatal("IngestEvent nil")
	}
	if m.RecipientUID != "ME" {
		t.Errorf("recipient = %q", m.RecipientUID)
	}
	if got := s.Conversation("ALICE-UID", nil); len(got) != 1 || got[0].Text != "hi bob" {
		t.Errorf("Conversation = %+v", got)
	}
}

func TestAppendOutgoingAndConversation(t *testing.T) {
	s := NewStore("ME")
	now := time.Now()
	out := s.AppendOutgoing("MSG-OUT", "BOB-UID", "BOB", "hello bob", "ME-CALL", now)
	if out.Status != StatusPending {
		t.Errorf("status = %v", out.Status)
	}
	if got := s.Conversation("BOB-UID", nil); len(got) != 1 || got[0].ID != "MSG-OUT" {
		t.Errorf("Conversation = %+v", got)
	}
	s.MarkStatus("MSG-OUT", StatusSent)
	if got := s.Conversation("BOB-UID", nil)[0].Status; got != StatusSent {
		t.Errorf("status after MarkStatus = %v", got)
	}
}

func TestAllChatFilterHidesIncomingFromBlockedSender(t *testing.T) {
	s := NewStore("ME")
	for _, who := range []string{"ALICE", "CHARLIE"} {
		ev, _ := cot.BuildGeoChat(
			cot.SelfInfo{UID: who, Callsign: who},
			cot.AllChat{},
			"hi from "+who,
			who+"-MSG",
			time.Now(),
		)
		s.IngestEvent(ev)
	}
	if got := s.AllChat(nil); len(got) != 2 {
		t.Fatalf("AllChat unfiltered = %d, want 2", len(got))
	}
	got := s.AllChat(func(uid string) bool { return uid != "CHARLIE" })
	if len(got) != 1 || got[0].SenderCallsign != "ALICE" {
		t.Errorf("filtered AllChat = %+v", got)
	}
}

func TestOutgoingNeverHiddenByFilter(t *testing.T) {
	s := NewStore("ME")
	now := time.Now()
	s.AppendOutgoing("X", "", "All Chat Rooms", "ours", "ME", now)
	// Filter that rejects everyone — outgoing should still come through.
	if got := s.AllChat(func(string) bool { return false }); len(got) != 1 {
		t.Errorf("outgoing message hidden by filter: %+v", got)
	}
}

func TestChronologicalOrder(t *testing.T) {
	s := NewStore("ME")
	t1 := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	ev2, _ := cot.BuildGeoChat(cot.SelfInfo{UID: "X", Callsign: "X"}, cot.AllChat{}, "second", "M2", t2)
	ev1, _ := cot.BuildGeoChat(cot.SelfInfo{UID: "X", Callsign: "X"}, cot.AllChat{}, "first", "M1", t1)
	s.IngestEvent(ev2)
	s.IngestEvent(ev1)
	got := s.AllChat(nil)
	if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Errorf("not chronological: %+v", got)
	}
}
