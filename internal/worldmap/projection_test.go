package worldmap

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectWorldExtents(t *testing.T) {
	cases := []struct {
		lat, lon          float64
		wantCol, wantRow  int
		width, height     int
	}{
		// Top-left of the canvas = -180, +90.
		{90, -180, 0, 0, 100, 50},
		// Bottom-right = +180, -90 (clamp to last cell).
		{-90, 180, 99, 49, 100, 50},
		// 0,0 lands in the middle-ish.
		{0, 0, 50, 25, 100, 50},
	}
	for _, c := range cases {
		col, row := World.Project(c.lat, c.lon, c.width, c.height)
		if col != c.wantCol || row != c.wantRow {
			t.Errorf("Project(%v,%v) = (%d,%d), want (%d,%d)", c.lat, c.lon, col, row, c.wantCol, c.wantRow)
		}
	}
}

func TestProjectClampsOutOfBounds(t *testing.T) {
	col, row := World.Project(120, 200, 100, 50)
	if col != 99 {
		t.Errorf("col = %d, want clamp to 99", col)
	}
	if row != 0 {
		t.Errorf("row = %d, want clamp to 0", row)
	}
}

func TestUnprojectIsInverseOfProject(t *testing.T) {
	const w, h = 200, 60
	for _, p := range []struct{ lat, lon float64 }{
		{59.33, 18.07},
		{-30, 145},
		{0, 0},
		{40, -75},
	} {
		col, row := World.Project(p.lat, p.lon, w, h)
		lat, lon := World.unproject(col, row, w, h)
		// Allow ~half-cell rounding error (cells are ~1.8 degrees lon, ~3 degrees lat).
		if abs(lat-p.lat) > 4 {
			t.Errorf("unproject lat err: got %v, want ~%v", lat, p.lat)
		}
		if abs(lon-p.lon) > 4 {
			t.Errorf("unproject lon err: got %v, want ~%v", lon, p.lon)
		}
	}
}

func TestRenderProducesExpectedDimensions(t *testing.T) {
	out := Render(World, 80, 20, nil)
	rows := strings.Split(out, "\n")
	if len(rows) != 20 {
		t.Errorf("rows = %d, want 20", len(rows))
	}
	for i, r := range rows {
		// Braille and other multi-byte glyphs make len(string) lie about
		// width; count runes instead.
		if got := utf8.RuneCountInString(r); got != 80 {
			t.Errorf("row %d width = %d runes, want 80", i, got)
		}
	}
}

func TestRenderPlacesMarker(t *testing.T) {
	markers := []Marker{
		{Lat: 0, Lon: 0, Rune: '@'},
	}
	out := Render(World, 81, 21, markers)
	rows := strings.Split(out, "\n")
	// 0,0 lands roughly at col 40, row 10 with this canvas size.
	mid := rows[10]
	if !strings.ContainsRune(mid, '@') {
		t.Errorf("middle row missing marker: %q", mid)
	}
}

func TestRenderDropsOutOfViewportMarkers(t *testing.T) {
	view := Viewport{MinLat: 50, MaxLat: 70, MinLon: 0, MaxLon: 30}
	markers := []Marker{
		{Lat: -30, Lon: 0, Rune: '@'},   // outside viewport
		{Lat: 60, Lon: 15, Rune: '#'},   // inside
	}
	out := Render(view, 30, 10, markers)
	if strings.ContainsRune(out, '@') {
		t.Error("out-of-viewport marker leaked into render")
	}
	if !strings.ContainsRune(out, '#') {
		t.Error("in-viewport marker missing")
	}
}

func TestAutoFitEmptyReturnsWorld(t *testing.T) {
	if got := AutoFit(nil); got != World {
		t.Errorf("AutoFit(nil) = %+v, want World", got)
	}
}

func TestAutoFitSinglePointWidens(t *testing.T) {
	v := AutoFit([]LatLon{{Lat: 59.33, Lon: 18.07}})
	if v.MaxLat-v.MinLat < 30 {
		t.Errorf("lat span = %v, want >=30", v.MaxLat-v.MinLat)
	}
	if v.MaxLon-v.MinLon < 30 {
		t.Errorf("lon span = %v, want >=30", v.MaxLon-v.MinLon)
	}
	// Centre should be near the input point.
	midLat := (v.MinLat + v.MaxLat) / 2
	midLon := (v.MinLon + v.MaxLon) / 2
	if abs(midLat-59.33) > 0.5 || abs(midLon-18.07) > 0.5 {
		t.Errorf("centre = %v,%v, want near 59.33,18.07", midLat, midLon)
	}
}

func TestAutoFitMultiplePointsAddsMargin(t *testing.T) {
	v := AutoFit([]LatLon{
		{Lat: 50, Lon: 0},
		{Lat: 60, Lon: 20},
	})
	// Span before margin: 10 lat, 20 lon. After 10% margin: 12 lat, 24 lon.
	latSpan := v.MaxLat - v.MinLat
	lonSpan := v.MaxLon - v.MinLon
	if latSpan < 11 || latSpan > 13 {
		t.Errorf("lat span = %v, want ~12", latSpan)
	}
	if lonSpan < 23 || lonSpan > 25 {
		t.Errorf("lon span = %v, want ~24", lonSpan)
	}
}

func TestAutoFitClampsToWorldBounds(t *testing.T) {
	v := AutoFit([]LatLon{{Lat: 89, Lon: 179}, {Lat: -89, Lon: -179}})
	if v.MinLat < -90 || v.MaxLat > 90 {
		t.Errorf("lat clamp: %v..%v", v.MinLat, v.MaxLat)
	}
	if v.MinLon < -180 || v.MaxLon > 180 {
		t.Errorf("lon clamp: %v..%v", v.MinLon, v.MaxLon)
	}
}

func TestPointInPolygonRectangle(t *testing.T) {
	rect := []LatLon{
		{Lat: 10, Lon: 10},
		{Lat: 10, Lon: 20},
		{Lat: 20, Lon: 20},
		{Lat: 20, Lon: 10},
	}
	if !pointInPolygon(15, 15, rect) {
		t.Error("centre point should be inside")
	}
	if pointInPolygon(0, 0, rect) {
		t.Error("origin should be outside")
	}
}

func TestBasemapLandShape(t *testing.T) {
	// At World viewport with reasonable size, somewhere on the Sweden /
	// southern-Norway block should produce a Braille land glyph rather than
	// a space. We sample a few cells around (60N, 18E) since the projection
	// + the 8-dot Braille granularity means the exact target cell may
	// occasionally fall in coastal water.
	canvas := Basemap(World, 240, 80)
	found := false
	for dr := -2; dr <= 2 && !found; dr++ {
		for dc := -2; dc <= 2 && !found; dc++ {
			col, row := World.Project(60, 18, 240, 80)
			r, c := row+dr, col+dc
			if r < 0 || r >= len(canvas) || c < 0 || c >= len(canvas[r]) {
				continue
			}
			if isBrailleLand(canvas[r][c]) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no Braille land glyph anywhere near Sweden at world zoom")
	}
}

func TestBasemapPacificIsOcean(t *testing.T) {
	canvas := Basemap(World, 240, 80)
	// Mid-Pacific at 0, -150 — should be either the equator gridline or
	// ocean, definitely not a Braille glyph.
	col, row := World.Project(0, -150, 240, 80)
	got := canvas[row][col]
	if isBrailleLand(got) {
		t.Errorf("Pacific cell rendered as land (rune %U)", got)
	}
}

// isBrailleLand reports whether r is a Braille pattern char other than
// the empty pattern U+2800 — i.e. at least one dot is set.
func isBrailleLand(r rune) bool {
	return r >= brailleBase+1 && r <= brailleBase+0xFF
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
