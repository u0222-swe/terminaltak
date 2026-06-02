package cot

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

const samplePLI = `<event version="2.0" uid="ANDROID-test123" type="a-f-G-U-C" how="m-g"` +
	` time="2026-04-29T10:00:00.000Z" start="2026-04-29T10:00:00.000Z" stale="2026-04-29T10:01:15.000Z">` +
	`<point lat="59.33" lon="18.07" hae="10" ce="9999999" le="9999999"/>` +
	`<detail>` +
	`<contact callsign="ALICE" endpoint="*:-1:stcp"/>` +
	`<takv device="iPhone" platform="iTAK" os="iOS" version="3.0"/>` +
	`<__group name="testchan_common" role="HQ"/>` +
	`<precisionlocation altsrc="GPS" geopointsrc="USER"/>` +
	`</detail>` +
	`</event>`

const sampleGeoChat = `<event version="2.0" uid="GeoChat.SENDER.All Chat Rooms.MSG-1" type="b-t-f"` +
	` how="h-g-i-g-o" time="2026-04-29T10:00:00.000Z" start="2026-04-29T10:00:00.000Z" stale="2026-04-30T10:00:00.000Z">` +
	`<point lat="59.33" lon="18.07" hae="0" ce="9999999" le="9999999"/>` +
	`<detail>` +
	`<__chat parent="RootContactGroup" groupOwner="false" chatroom="All Chat Rooms" id="All Chat Rooms" senderCallsign="ALICE">` +
	`<chatgrp uid0="SENDER" uid1="All Chat Rooms" id="All Chat Rooms"/>` +
	`</__chat>` +
	`<link uid="SENDER" type="a-f-G-U-C" relation="p-p"/>` +
	`<remarks source="BAO.F.TerminalTAK.SENDER" to="All Chat Rooms" time="2026-04-29T10:00:00.000Z">hello world</remarks>` +
	`</detail>` +
	`</event>`

func TestDecodeSinglePLI(t *testing.T) {
	events, errs := Decode(strings.NewReader(samplePLI))
	ev := <-events
	if ev.UID != "ANDROID-test123" {
		t.Errorf("UID = %q", ev.UID)
	}
	if ev.Type != "a-f-G-U-C" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.Affiliation() != AffiliationFriendly {
		t.Errorf("Affiliation = %v, want Friendly", ev.Affiliation())
	}
	if ev.Domain() != DomainGround {
		t.Errorf("Domain = %v, want Ground", ev.Domain())
	}
	if ev.Point.Lat != 59.33 || ev.Point.Lon != 18.07 {
		t.Errorf("Point = %+v", ev.Point)
	}
	if ev.Detail.Contact == nil || ev.Detail.Contact.Callsign != "ALICE" {
		t.Errorf("Contact = %+v", ev.Detail.Contact)
	}
	if ev.Detail.Group == nil || ev.Detail.Group.Name != "testchan_common" || ev.Detail.Group.Role != "HQ" {
		t.Errorf("Group = %+v", ev.Detail.Group)
	}
	if err, ok := <-errs; ok {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeMultipleConcatenated(t *testing.T) {
	stream := samplePLI + "\n" + sampleGeoChat + "\n" + samplePLI
	events, errs := Decode(strings.NewReader(stream))
	count := 0
	for ev := range events {
		count++
		if count == 2 && !ev.IsGeoChat() {
			t.Errorf("event 2 should be GeoChat, got type %q", ev.Type)
		}
	}
	if count != 3 {
		t.Errorf("decoded %d events, want 3", count)
	}
	if err, ok := <-errs; ok {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeMalformed(t *testing.T) {
	events, errs := Decode(strings.NewReader("<event uid=\"x\""))
	if _, ok := <-events; ok {
		t.Error("expected no events on malformed input")
	}
	err, ok := <-errs
	if !ok {
		t.Fatal("expected error on malformed input")
	}
	if err == nil {
		t.Error("expected non-nil error")
	}
}

func TestEncodeRoundTripPLI(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	self := SelfInfo{
		UID:      "11111111-2222-4333-8444-555555555555",
		Callsign: "ALICE",
		Group:    "testchank_common",
		Role:     "HQ",
		Lat:      59.33,
		Lon:      18.07,
		HAE:      10,
	}
	original := BuildPLI(self, 75*time.Second, now)

	var buf bytes.Buffer
	if err := Encode(&buf, original); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	events, errs := Decode(&buf)
	got := <-events
	if got.UID != self.UID {
		t.Errorf("UID = %q", got.UID)
	}
	if got.Detail.Group == nil || got.Detail.Group.Name != self.Group {
		t.Errorf("Group lost in round-trip: %+v", got.Detail.Group)
	}
	if got.Point.Lat != self.Lat || got.Point.Lon != self.Lon {
		t.Errorf("Point = %+v, want lat=%v lon=%v", got.Point, self.Lat, self.Lon)
	}
	wantStale := now.Add(75 * time.Second).Format(TimeFormat)
	if got.Stale != wantStale {
		t.Errorf("Stale = %q, want %q", got.Stale, wantStale)
	}
	if err, ok := <-errs; ok {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildGeoChatAllChat(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	self := SelfInfo{
		UID:      "SENDER-UID",
		Callsign: "ALICE",
		Lat:      59.33, Lon: 18.07,
	}
	ev, err := BuildGeoChat(self, AllChat{}, "hello world", "MSG-1", now)
	if err != nil {
		t.Fatalf("BuildGeoChat: %v", err)
	}
	if ev.UID != "GeoChat.SENDER-UID.All Chat Rooms.MSG-1" {
		t.Errorf("UID = %q", ev.UID)
	}
	if ev.Type != "b-t-f" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.Detail.Chat == nil || ev.Detail.Chat.Chatroom != "All Chat Rooms" {
		t.Errorf("Chat = %+v", ev.Detail.Chat)
	}
	if ev.Detail.Remarks == nil || ev.Detail.Remarks.Text != "hello world" {
		t.Errorf("Remarks = %+v", ev.Detail.Remarks)
	}
	if ev.Detail.Marti != nil {
		t.Errorf("All-Chat should not include <marti/>, got %+v", ev.Detail.Marti)
	}
	if !ev.IsGeoChat() {
		t.Error("IsGeoChat() = false")
	}
}

func TestBuildGeoChatDM(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	self := SelfInfo{UID: "SENDER-UID", Callsign: "ALICE"}
	dest := DM{RecipientUID: "RECIP-UID", RecipientCallsign: "BOB"}
	ev, err := BuildGeoChat(self, dest, "hi bob", "MSG-2", now)
	if err != nil {
		t.Fatalf("BuildGeoChat: %v", err)
	}
	if ev.UID != "GeoChat.SENDER-UID.RECIP-UID.MSG-2" {
		t.Errorf("UID = %q", ev.UID)
	}
	if ev.Detail.Chat == nil || ev.Detail.Chat.ID != "RECIP-UID" {
		t.Errorf("Chat.ID = %q", ev.Detail.Chat.ID)
	}
	if ev.Detail.Marti == nil || len(ev.Detail.Marti.Dests) != 1 || ev.Detail.Marti.Dests[0].Callsign != "BOB" {
		t.Errorf("Marti = %+v", ev.Detail.Marti)
	}
}

func TestEncodeRoundTripGeoChat(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	self := SelfInfo{UID: "SENDER-UID", Callsign: "ALICE", Lat: 59.33, Lon: 18.07}
	original, err := BuildGeoChat(self, AllChat{}, "hello world", "MSG-3", now)
	if err != nil {
		t.Fatalf("BuildGeoChat: %v", err)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, original); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	events, _ := Decode(&buf)
	got := <-events
	if got.Detail.Remarks.Text != "hello world" {
		t.Errorf("text = %q after round-trip", got.Detail.Remarks.Text)
	}
}

func TestDecodePreservesUnknownDetailViaRoundTrip(t *testing.T) {
	const withUnknown = `<event version="2.0" uid="x" type="a-f-G-U-C" time="2026-04-29T10:00:00.000Z"` +
		` start="2026-04-29T10:00:00.000Z" stale="2026-04-29T10:01:00.000Z">` +
		`<point lat="0" lon="0" hae="0" ce="0" le="0"/>` +
		`<detail><__future_thing custom="yes">payload</__future_thing></detail>` +
		`</event>`
	events, _ := Decode(strings.NewReader(withUnknown))
	ev := <-events
	if len(ev.Detail.Other) != 1 {
		t.Fatalf("Other = %v, want 1 element", ev.Detail.Other)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, ev); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(buf.String(), "__future_thing") {
		t.Errorf("encoded form missing __future_thing: %s", buf.String())
	}
}

func TestDecodeEOFClosesCleanly(t *testing.T) {
	events, errs := Decode(strings.NewReader(""))
	for range events {
	}
	if err, ok := <-errs; ok {
		t.Errorf("expected no error on empty stream, got %v", err)
	}
}

// pipeReader wraps an io.Reader so we can simulate a slow drip-feed for
// streaming behaviour tests.
type pipeReader struct{ chunks [][]byte }

func (p *pipeReader) Read(b []byte) (int, error) {
	if len(p.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.chunks[0])
	if n == len(p.chunks[0]) {
		p.chunks = p.chunks[1:]
	} else {
		p.chunks[0] = p.chunks[0][n:]
	}
	return n, nil
}

func TestDecodeStreamsAcrossReadBoundaries(t *testing.T) {
	first := []byte(samplePLI[:100])
	rest := []byte(samplePLI[100:])
	r := &pipeReader{chunks: [][]byte{first, rest}}
	events, errs := Decode(r)
	ev := <-events
	if ev.UID != "ANDROID-test123" {
		t.Errorf("UID = %q after split read", ev.UID)
	}
	if err, ok := <-errs; ok {
		t.Errorf("unexpected error: %v", err)
	}
}
