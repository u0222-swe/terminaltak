package cot

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestMarkerType(t *testing.T) {
	cases := map[Affiliation]string{
		AffiliationFriendly:      "a-f-G",
		AffiliationAssumedFriend: "a-f-G",
		AffiliationHostile:       "a-h-G",
		AffiliationNeutral:       "a-n-G",
		AffiliationUnknown:       "a-u-G",
		AffiliationSuspect:       "a-u-G", // unmapped -> unknown
	}
	for aff, want := range cases {
		if got := MarkerType(aff); got != want {
			t.Errorf("MarkerType(%v) = %q, want %q", aff, got, want)
		}
	}
}

func TestBuildMarker(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	ev, uid, err := BuildMarker("creator-123", "", "a-h-G", "Tgt-1", "two trucks", 59.33, 18.07, 12, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("BuildMarker: %v", err)
	}
	if uid == "" {
		t.Fatal("expected a generated uid")
	}
	if ev.UID != uid || ev.Type != "a-h-G" {
		t.Errorf("uid/type = %q/%q", ev.UID, ev.Type)
	}
	if ev.Detail.Contact == nil || ev.Detail.Contact.Callsign != "Tgt-1" {
		t.Errorf("contact callsign not set: %+v", ev.Detail.Contact)
	}
	if ev.Detail.Remarks == nil || ev.Detail.Remarks.Text != "two trucks" {
		t.Errorf("remarks not set: %+v", ev.Detail.Remarks)
	}
	if ev.Detail.Link == nil || ev.Detail.Link.UID != "creator-123" {
		t.Errorf("creator link not set: %+v", ev.Detail.Link)
	}
	if ev.Point.Lat != 59.33 || ev.Point.Lon != 18.07 {
		t.Errorf("point = %v", ev.Point)
	}
	// The <archive/> marker survives a round-trip through the encoder.
	var buf bytes.Buffer
	if err := Encode(&buf, ev); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(buf.String(), "<archive>") && !strings.Contains(buf.String(), "<archive/>") && !strings.Contains(buf.String(), "<archive ") {
		t.Errorf("expected <archive/> in encoded marker, got: %s", buf.String())
	}
}

func TestBuildMarkerKeepsGivenUID(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	ev, uid, err := BuildMarker("c", "fixed-uid", "a-f-G", "x", "", 1, 2, 0, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if uid != "fixed-uid" || ev.UID != "fixed-uid" {
		t.Errorf("uid = %q / %q, want fixed-uid", uid, ev.UID)
	}
	if ev.Detail.Remarks != nil {
		t.Errorf("empty remarks should be omitted, got %+v", ev.Detail.Remarks)
	}
}

func TestBuildMarkerDelete(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	ev, err := BuildMarkerDelete("target-9", "a-h-G", now)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "t-x-d-d" {
		t.Errorf("type = %q, want t-x-d-d", ev.Type)
	}
	if ev.Detail.Link == nil || ev.Detail.Link.UID != "target-9" {
		t.Errorf("delete link not set: %+v", ev.Detail.Link)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, ev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "forcedelete") {
		t.Errorf("expected __forcedelete in encoded delete, got: %s", buf.String())
	}
}
