package worldmap

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// landGeoJSON is Natural Earth's ne_110m_land dataset (public domain). It
// contains ~127 closed polygon rings outlining the world's land masses at
// 1:110m scale — ideal for terminal-resolution rendering. Source:
// https://github.com/martynafford/natural-earth-geojson
//
//go:embed ne_110m_land.json
var landGeoJSON []byte

// landPolygons is the parsed dataset, cached after the first call.
var (
	landOnce     sync.Once
	landPolygons [][]LatLon
	landBoxes    []bbox // per-polygon bounding box for fast rejection
)

// bbox is the axis-aligned latitude/longitude rectangle that contains a
// polygon. We test a candidate point against the bbox first because
// pointInPolygon is ~10× more expensive.
type bbox struct {
	minLat, maxLat, minLon, maxLon float64
}

// loadLand parses the embedded GeoJSON on first use. The dataset uses
// (lon, lat) coordinate order per the GeoJSON spec; we convert to our
// internal (lat, lon) LatLon struct on the way in. MultiPolygon features
// are flattened into one entry per outer ring.
func loadLand() {
	landOnce.Do(func() {
		var fc struct {
			Features []struct {
				Geometry struct {
					Type        string          `json:"type"`
					Coordinates json.RawMessage `json:"coordinates"`
				} `json:"geometry"`
			} `json:"features"`
		}
		if err := json.Unmarshal(landGeoJSON, &fc); err != nil {
			return
		}
		for _, f := range fc.Features {
			switch f.Geometry.Type {
			case "Polygon":
				var rings [][][2]float64
				if err := json.Unmarshal(f.Geometry.Coordinates, &rings); err != nil {
					continue
				}
				if len(rings) > 0 {
					addRing(rings[0])
				}
			case "MultiPolygon":
				var polys [][][][2]float64
				if err := json.Unmarshal(f.Geometry.Coordinates, &polys); err != nil {
					continue
				}
				for _, rings := range polys {
					if len(rings) > 0 {
						addRing(rings[0])
					}
				}
			}
		}
	})
}

func addRing(coords [][2]float64) {
	if len(coords) < 3 {
		return
	}
	ring := make([]LatLon, 0, len(coords))
	first := true
	var b bbox
	for _, c := range coords {
		lon, lat := c[0], c[1]
		ring = append(ring, LatLon{Lat: lat, Lon: lon})
		if first {
			b.minLat, b.maxLat = lat, lat
			b.minLon, b.maxLon = lon, lon
			first = false
			continue
		}
		if lat < b.minLat {
			b.minLat = lat
		}
		if lat > b.maxLat {
			b.maxLat = lat
		}
		if lon < b.minLon {
			b.minLon = lon
		}
		if lon > b.maxLon {
			b.maxLon = lon
		}
	}
	landPolygons = append(landPolygons, ring)
	landBoxes = append(landBoxes, b)
}

// pointInLand returns true if (lat, lon) falls inside any land polygon.
// Each polygon's bounding box is checked first — only polygons that pass
// the bbox test are run through the full ray-cast.
func pointInLand(lat, lon float64) bool {
	loadLand()
	for i, b := range landBoxes {
		if lat < b.minLat || lat > b.maxLat || lon < b.minLon || lon > b.maxLon {
			continue
		}
		if pointInPolygon(lat, lon, landPolygons[i]) {
			return true
		}
	}
	return false
}
