package mgrs

import (
	"math"
	"testing"
)

// approx compares two floats and returns true when they agree within tol.
func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestRoundTripStockholm(t *testing.T) {
	// Stockholm city centre — well-known reference.
	const lat, lon = 59.3293, 18.0686
	grid, err := Format(lat, lon)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	t.Logf("Stockholm MGRS: %s", grid)

	gotLat, gotLon, err := Parse(grid)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// 1m precision means we expect agreement within a few thousandths of a
	// degree at this latitude.
	if !approx(gotLat, lat, 0.01) {
		t.Errorf("lat round-trip drift: got %v want %v", gotLat, lat)
	}
	if !approx(gotLon, lon, 0.01) {
		t.Errorf("lon round-trip drift: got %v want %v", gotLon, lon)
	}
}

func TestParseAcceptsSpacedAndCompact(t *testing.T) {
	cases := []string{
		"33V XM 8120 1530",
		"33VXM81201530",
		"33V XM81201530",
		"33VXM 8120 1530",
	}
	var first [2]float64
	for i, c := range cases {
		lat, lon, err := Parse(c)
		if err != nil {
			t.Fatalf("Parse %q: %v", c, err)
		}
		if i == 0 {
			first = [2]float64{lat, lon}
			continue
		}
		if !approx(lat, first[0], 1e-6) || !approx(lon, first[1], 1e-6) {
			t.Errorf("parse %q gave (%v,%v), want (%v,%v)", c, lat, lon, first[0], first[1])
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"33",                 // too short
		"33V",                // missing grid square
		"33V X",              // half grid
		"99V XM 0 0",         // zone out of range
		"33I XM 0 0",         // I is not a valid lat band
		"33V XM 1 12",        // mismatched digit counts
	}
	for _, s := range bad {
		if _, _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should error", s)
		}
	}
}

func TestParsePrecisionWidens(t *testing.T) {
	// Test that lower precision (fewer digits) still parses; the result
	// should land inside the 100km square.
	hi, _, err := Parse("33V XM 8120 1530") // 1 m
	if err != nil {
		t.Fatalf("hi-precision parse: %v", err)
	}
	lo, _, err := Parse("33V XM 81 15") // 1 km
	if err != nil {
		t.Fatalf("lo-precision parse: %v", err)
	}
	// At 1 km precision the centre should be within ~1km (~0.01 degrees) of
	// the higher-precision answer.
	if !approx(hi, lo, 0.02) {
		t.Errorf("low-precision parse drifted too far: hi=%v lo=%v", hi, lo)
	}
}

func TestLatBandLetter(t *testing.T) {
	cases := map[float64]byte{
		-80.0: 'C',
		-70.0: 'D',
		0.0:   'N',
		8.0:   'P', // P is the 'I' skip
		60.0:  'V',
		70.0:  'W',
		78.0:  'X',
	}
	for lat, want := range cases {
		if got := latBandLetter(lat); got != want {
			t.Errorf("latBandLetter(%v) = %c, want %c", lat, got, want)
		}
	}
}
