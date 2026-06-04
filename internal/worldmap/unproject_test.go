package worldmap

import (
	"math"
	"testing"
)

func TestUnprojectRoundTrip(t *testing.T) {
	v := Viewport{MinLat: 55, MaxLat: 69, MinLon: 11, MaxLon: 24}
	const w, h = 80, 24
	// Project the centre of a handful of cells and confirm Unproject lands
	// back in the same cell.
	for _, cell := range [][2]int{{0, 0}, {40, 12}, {79, 23}, {10, 5}} {
		col, row := cell[0], cell[1]
		lat, lon := v.Unproject(col, row, w, h)
		gotCol, gotRow := v.Project(lat, lon, w, h)
		if gotCol != col || gotRow != row {
			t.Errorf("cell (%d,%d) -> (%.4f,%.4f) -> (%d,%d)", col, row, lat, lon, gotCol, gotRow)
		}
	}
}

func TestUnprojectCentre(t *testing.T) {
	v := Viewport{MinLat: -10, MaxLat: 10, MinLon: -20, MaxLon: 20}
	lat, lon := v.Unproject(50, 25, 100, 50) // dead centre cell
	if math.Abs(lat-0) > 0.5 || math.Abs(lon-0) > 0.5 {
		t.Errorf("centre cell = (%.4f,%.4f), want ~(0,0)", lat, lon)
	}
}
