package worldmap

import "sync"

// Basemap returns a width×height canvas of runes with the world's land
// rendered as Braille glyphs (Unicode U+2800–U+28FF). Each cell holds a
// 2×4 sub-pixel grid so the apparent resolution is 8× the canvas size,
// giving recognisable continent silhouettes even at small terminal
// dimensions.
//
// The land polygons are sourced from Natural Earth's land datasets (public
// domain) at three scales, embedded gzip-compressed via go:embed in
// landdata.go and chosen by zoom (lodForSpan). Rendering is fully procedural
// so the same code handles full-globe and zoomed views; the land layer is
// memoised per viewport (see cachedLandCanvas) to keep the per-frame cost low.
//
// Equator and prime meridian gridlines are overlaid afterwards as plain
// '=' / '|' characters — they trample empty cells but never the Braille
// land glyphs, so the coastline stays intact.
func Basemap(viewport Viewport, width, height int) [][]rune {
	canvas := cachedLandCanvas(viewport, width, height)
	if width <= 0 || height <= 0 {
		return canvas
	}
	drawParallels(canvas, viewport, []float64{0}, '=')
	drawParallels(canvas, viewport, []float64{-60, -30, 30, 60}, '-')
	drawMeridians(canvas, viewport, []float64{0}, '|')
	return canvas
}

// basemapKey identifies a rendered land layer. The land render is the
// expensive step (a point-in-land test per Braille sub-dot), so we memoise
// the most recent result: the TUI re-renders several times a second, but the
// viewport only changes on zoom / pan / contact movement. A single-slot cache
// is enough because exactly one viewport is on screen at a time.
type basemapKey struct {
	lod                            LOD
	minLat, maxLat, minLon, maxLon float64
	width, height                  int
}

var (
	bmMu     sync.Mutex
	bmValid  bool
	bmKey    basemapKey
	bmMaster [][]rune // land-only layer (no gridlines/markers); never mutated
)

// cachedLandCanvas returns a fresh, mutable copy of the land layer for the
// viewport, rendering (and caching) it on a miss. Callers may freely draw
// gridlines and markers onto the returned canvas without disturbing the
// cached master.
func cachedLandCanvas(viewport Viewport, width, height int) [][]rune {
	lod := lodForSpan(viewport)
	key := basemapKey{lod, viewport.MinLat, viewport.MaxLat, viewport.MinLon, viewport.MaxLon, width, height}

	bmMu.Lock()
	if bmValid && bmKey == key {
		c := copyCanvas(bmMaster)
		bmMu.Unlock()
		return c
	}
	bmMu.Unlock()

	master := renderBrailleBasemap(viewport, width, height, lod)

	bmMu.Lock()
	bmKey = key
	bmMaster = master
	bmValid = true
	c := copyCanvas(master)
	bmMu.Unlock()
	return c
}

func copyCanvas(src [][]rune) [][]rune {
	dst := make([][]rune, len(src))
	for i, row := range src {
		dst[i] = make([]rune, len(row))
		copy(dst[i], row)
	}
	return dst
}

func (v Viewport) unproject(col, row, width, height int) (lat, lon float64) {
	if width == 0 || height == 0 {
		return 0, 0
	}
	xFrac := (float64(col) + 0.5) / float64(width)
	yFrac := (float64(row) + 0.5) / float64(height)
	lon = v.MinLon + xFrac*(v.MaxLon-v.MinLon)
	lat = v.MaxLat - yFrac*(v.MaxLat-v.MinLat)
	return lat, lon
}

func drawParallels(canvas [][]rune, v Viewport, lats []float64, ch rune) {
	height := len(canvas)
	if height == 0 {
		return
	}
	width := len(canvas[0])
	for _, lat := range lats {
		if lat < v.MinLat || lat > v.MaxLat {
			continue
		}
		_, row := v.Project(lat, (v.MinLon+v.MaxLon)/2, width, height)
		for col := 0; col < width; col++ {
			if canvas[row][col] == ' ' {
				canvas[row][col] = ch
			}
		}
	}
}

func drawMeridians(canvas [][]rune, v Viewport, lons []float64, ch rune) {
	height := len(canvas)
	if height == 0 {
		return
	}
	width := len(canvas[0])
	for _, lon := range lons {
		if lon < v.MinLon || lon > v.MaxLon {
			continue
		}
		col, _ := v.Project((v.MinLat+v.MaxLat)/2, lon, width, height)
		for row := 0; row < height; row++ {
			if canvas[row][col] == ' ' {
				canvas[row][col] = ch
			}
		}
	}
}

// pointInPolygon uses ray-casting (Jordan curve theorem). poly is a closed
// ring of (lat, lon); the closing edge is implicit.
func pointInPolygon(lat, lon float64, poly []LatLon) bool {
	inside := false
	n := len(poly)
	if n < 3 {
		return false
	}
	j := n - 1
	for i := 0; i < n; i++ {
		yi, xi := poly[i].Lat, poly[i].Lon
		yj, xj := poly[j].Lat, poly[j].Lon
		if (yi > lat) != (yj > lat) {
			xIntersect := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < xIntersect {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}
