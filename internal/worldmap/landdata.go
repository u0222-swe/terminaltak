package worldmap

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"math"
	"sync"
)

// Natural Earth land datasets (public domain) at three scales, embedded
// gzip-compressed to keep the binary small and decompressed lazily on first
// use. Source: https://github.com/martynafford/natural-earth-geojson
//
//   - 110m: ~127 polygons, coarse — used for world / continental views.
//   - 50m:  medium detail — used when zoomed to a country / region.
//   - 10m:  fine detail (bays, islands, fjords) — used at high zoom.
//
// The renderer picks the scale from the viewport span (see lodForSpan), so a
// zoomed-in map gets a sharper coastline without paying the 10m parse/RAM
// cost until it is actually needed.
//
//go:embed ne_110m_land.json.gz
var land110gz []byte

//go:embed ne_50m_land.json.gz
var land50gz []byte

//go:embed ne_10m_land.json.gz
var land10gz []byte

// LOD identifies a land-detail level.
type LOD int

const (
	LOD110 LOD = iota // coarse — world / continental
	LOD50             // medium — country / region
	LOD10             // fine — high zoom
)

// lodForSpan picks the land dataset for a viewport based on its larger
// geographic span (degrees). The thresholds are tuned so a full-globe view
// uses 110m, a country-sized view uses 50m, and a tight regional view uses
// 10m.
func lodForSpan(v Viewport) LOD {
	span := math.Max(v.MaxLat-v.MinLat, v.MaxLon-v.MinLon)
	switch {
	case span > 25:
		return LOD110
	case span > 4:
		return LOD50
	default:
		return LOD10
	}
}

// landIndex is a flattened, query-optimised form of a land dataset: every
// polygon edge as a segment, bucketed into latitude bands so a point-in-land
// test only inspects the handful of edges crossing the query latitude rather
// than every edge in the world. Coordinates are float32 — 10m precision is
// ~1e-4°, well within float32's ~7 significant digits — which roughly halves
// the resident memory of the (large) 10m index.
type landIndex struct {
	lat0, lon0 []float32
	lat1, lon1 []float32

	minLat, maxLat float64
	invBand        float64 // nBands / (maxLat-minLat)
	nBands         int
	bands          [][]int32 // band -> edge indices crossing that band
}

// lazily-built indices, one per LOD.
var (
	lodOnce [3]sync.Once
	lodIdx  [3]*landIndex
)

func gzipData(lod LOD) []byte {
	switch lod {
	case LOD50:
		return land50gz
	case LOD10:
		return land10gz
	default:
		return land110gz
	}
}

// indexFor returns the (lazily built) edge index for lod, or nil if the
// dataset failed to decode.
func indexFor(lod LOD) *landIndex {
	i := int(lod)
	lodOnce[i].Do(func() {
		lodIdx[i] = buildIndex(gzipData(lod))
	})
	return lodIdx[i]
}

// buildIndex gunzips and parses a Natural Earth GeoJSON land file, then
// flattens every outer ring into edge segments bucketed by latitude band.
// Inner rings (lakes) are ignored, matching the original renderer — lakes
// render as land. Returns nil on any decode error.
func buildIndex(gz []byte) *landIndex {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil
	}

	idx := &landIndex{minLat: math.Inf(1), maxLat: math.Inf(-1)}
	addRing := func(coords [][2]float64) {
		n := len(coords)
		if n < 3 {
			return
		}
		for k := 0; k < n; k++ {
			a := coords[k]
			b := coords[(k+1)%n] // closing edge implicit
			idx.lon0 = append(idx.lon0, float32(a[0]))
			idx.lat0 = append(idx.lat0, float32(a[1]))
			idx.lon1 = append(idx.lon1, float32(b[0]))
			idx.lat1 = append(idx.lat1, float32(b[1]))
			for _, lat := range []float64{a[1], b[1]} {
				if lat < idx.minLat {
					idx.minLat = lat
				}
				if lat > idx.maxLat {
					idx.maxLat = lat
				}
			}
		}
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

	nEdges := len(idx.lat0)
	if nEdges == 0 || !(idx.maxLat > idx.minLat) {
		return nil
	}
	// Band count scales with edge density: enough bands that each holds a
	// small slice, clamped to a sane range.
	idx.nBands = clampInt(nEdges/50, 128, 8192)
	idx.invBand = float64(idx.nBands) / (idx.maxLat - idx.minLat)
	idx.bands = make([][]int32, idx.nBands)
	for e := 0; e < nEdges; e++ {
		latA, latB := float64(idx.lat0[e]), float64(idx.lat1[e])
		lo, hi := latA, latB
		if hi < lo {
			lo, hi = hi, lo
		}
		bLo := idx.bandOf(lo)
		bHi := idx.bandOf(hi)
		for b := bLo; b <= bHi; b++ {
			idx.bands[b] = append(idx.bands[b], int32(e))
		}
	}
	return idx
}

func (idx *landIndex) bandOf(lat float64) int {
	b := int((lat - idx.minLat) * idx.invBand)
	if b < 0 {
		return 0
	}
	if b >= idx.nBands {
		return idx.nBands - 1
	}
	return b
}

// pointInLand reports whether (lat, lon) is on land at the given detail level.
// Because Natural Earth's land polygons are non-overlapping, a global
// even-odd crossing count over the edges in the point's latitude band yields
// the union membership directly — no per-polygon bookkeeping needed.
func pointInLand(lat, lon float64, lod LOD) bool {
	idx := indexFor(lod)
	if idx == nil || lat < idx.minLat || lat > idx.maxLat {
		return false
	}
	inside := false
	for _, e := range idx.bands[idx.bandOf(lat)] {
		yi := float64(idx.lat0[e])
		yj := float64(idx.lat1[e])
		if (yi > lat) != (yj > lat) {
			xi := float64(idx.lon0[e])
			xj := float64(idx.lon1[e])
			x := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < x {
				inside = !inside
			}
		}
	}
	return inside
}

func clampInt(x, lo, hi int) int {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
