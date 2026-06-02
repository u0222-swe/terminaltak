package worldmap

// Basemap returns a width×height canvas of runes with the world's land
// rendered as Braille glyphs (Unicode U+2800–U+28FF). Each cell holds a
// 2×4 sub-pixel grid so the apparent resolution is 8× the canvas size,
// giving recognisable continent silhouettes even at small terminal
// dimensions.
//
// The land polygons are sourced from Natural Earth's ne_110m_land dataset
// (public domain, ~127 polygons, ~3 000 vertices), embedded into the
// binary via go:embed in landdata.go. Rendering is fully procedural so the
// same code handles full-globe and zoomed views.
//
// Equator and prime meridian gridlines are overlaid afterwards as plain
// '=' / '|' characters — they trample empty cells but never the Braille
// land glyphs, so the coastline stays intact.
func Basemap(viewport Viewport, width, height int) [][]rune {
	canvas := renderBrailleBasemap(viewport, width, height)
	if width <= 0 || height <= 0 {
		return canvas
	}
	drawParallels(canvas, viewport, []float64{0}, '=')
	drawParallels(canvas, viewport, []float64{-60, -30, 30, 60}, '-')
	drawMeridians(canvas, viewport, []float64{0}, '|')
	return canvas
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
