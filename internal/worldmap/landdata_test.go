package worldmap

import "testing"

func TestLodForSpan(t *testing.T) {
	cases := []struct {
		span float64
		want LOD
	}{
		{180, LOD110},
		{40, LOD110},
		{25.1, LOD110},
		{20, LOD50},
		{5, LOD50},
		{4.1, LOD50},
		{4, LOD10},
		{0.5, LOD10},
	}
	for _, c := range cases {
		v := Viewport{MinLat: 0, MaxLat: c.span, MinLon: 0, MaxLon: c.span / 2}
		if got := lodForSpan(v); got != c.want {
			t.Errorf("span %.1f: lod = %d, want %d", c.span, got, c.want)
		}
	}
}

func TestIndexBuildsForEveryLOD(t *testing.T) {
	for _, lod := range []LOD{LOD110, LOD50, LOD10} {
		idx := indexFor(lod)
		if idx == nil {
			t.Fatalf("lod %d: index is nil (dataset failed to decode)", lod)
		}
		if len(idx.lat0) == 0 {
			t.Errorf("lod %d: no edges", lod)
		}
		if !(idx.maxLat > idx.minLat) || idx.minLat < -91 || idx.maxLat > 91 {
			t.Errorf("lod %d: bad lat range [%.2f, %.2f]", lod, idx.minLat, idx.maxLat)
		}
	}
}

func TestPointInLand(t *testing.T) {
	// Central Poland — solidly inland at every scale.
	land := struct{ lat, lon float64 }{52.0, 19.0}
	// Mid-Pacific and mid-Atlantic — open ocean.
	sea := []struct{ lat, lon float64 }{{0, -140}, {-20, -10}}

	for _, lod := range []LOD{LOD110, LOD50, LOD10} {
		if !pointInLand(land.lat, land.lon, lod) {
			t.Errorf("lod %d: (%.1f,%.1f) should be land", lod, land.lat, land.lon)
		}
		for _, s := range sea {
			if pointInLand(s.lat, s.lon, lod) {
				t.Errorf("lod %d: (%.1f,%.1f) should be sea", lod, s.lat, s.lon)
			}
		}
	}
}

func TestHigherLODResolvesFinerCoast(t *testing.T) {
	// Render a tight viewport over the Stockholm archipelago at each scale and
	// confirm finer datasets light up at least as many land sub-dots — a proxy
	// for "more coastline detail when zoomed in".
	v := Viewport{MinLat: 59.0, MaxLat: 59.6, MinLon: 18.0, MaxLon: 19.0}
	const w, h = 120, 40
	count := func(lod LOD) int {
		canvas := renderBrailleBasemap(v, w, h, lod)
		n := 0
		for _, row := range canvas {
			for _, r := range row {
				if r != ' ' {
					n++
				}
			}
		}
		return n
	}
	c110, c10 := count(LOD110), count(LOD10)
	if c10 < c110 {
		t.Errorf("expected 10m to render at least as much detail as 110m, got 10m=%d 110m=%d", c10, c110)
	}
}
