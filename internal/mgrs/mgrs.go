// Package mgrs converts between MGRS grid references and decimal lat/lon
// using the standard NATO grid encoding (NIMA TM 8358.1, MIL-STD 2401).
//
// Supported MGRS formats:
//
//	33V XK 8200 1500
//	33VXK82001500
//	33VXK 82001500
//	33V XK82001500
//
// The grid-square pair must be exactly 2 letters; the easting and northing
// segments must each be 1–5 digits with the same length (i.e. 1m
// resolution = 5 digits, 10m = 4, 100m = 3, 1km = 2, 10km = 1).
//
// UTM ↔ lat/lon math is delegated to github.com/im7mortal/UTM, which is a
// small pure-Go port of the standard transverse-Mercator forward/inverse
// formulae. This package just handles the MGRS grid square encoding on top.
package mgrs

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	utm "github.com/im7mortal/UTM"
)

// Two letters omitted from MGRS column letters: I and O (avoid confusion
// with the digits 1 and 0). MGRS row letters omit the same two.
const (
	colLetters = "ABCDEFGHJKLMNPQRSTUVWXYZ" // 24 letters, repeats every 3 zones
	rowLetters = "ABCDEFGHJKLMNPQRSTUV"     // 20 letters, wraps every 2,000,000 m
)

// Parse returns the latitude and longitude in degrees of the centre of an
// MGRS grid square (or sub-square at the supplied resolution).
func Parse(s string) (lat, lon float64, err error) {
	zone, latBand, gridSq, easting, northing, resolution, err := parseMGRS(s)
	if err != nil {
		return 0, 0, err
	}
	utmEasting, utmNorthing, err := gridToUTM(zone, latBand, gridSq, easting, northing)
	if err != nil {
		return 0, 0, err
	}
	// Centre on the resolution box.
	utmEasting += float64(resolution) / 2
	utmNorthing += float64(resolution) / 2

	northern := isNorthernHemisphere(latBand)
	lat, lon, err = utm.ToLatLon(utmEasting, utmNorthing, zone, "", northern)
	if err != nil {
		return 0, 0, fmt.Errorf("utm to lat/lon: %w", err)
	}
	return lat, lon, nil
}

// Format renders a lat/lon as an MGRS string at 5-digit (1 m) precision.
func Format(lat, lon float64) (string, error) {
	easting, northing, zone, _, err := utm.FromLatLon(lat, lon, lat >= 0)
	if err != nil {
		return "", fmt.Errorf("lat/lon to utm: %w", err)
	}
	band := latBandLetter(lat)
	gridSq, eastingMod, northingMod := utmToGridSquare(zone, easting, northing)

	// 5-digit segments.
	easting5 := int(math.Mod(eastingMod, 100000))
	northing5 := int(math.Mod(northingMod, 100000))
	return fmt.Sprintf("%d%c %s %05d %05d", zone, band, gridSq, easting5, northing5), nil
}

// parseMGRS strips whitespace and decomposes s into its components. The
// resolution is the size in metres of the grid box implied by the digit
// counts (e.g. 5+5 digits = 1 m).
func parseMGRS(s string) (zone int, latBand byte, gridSq string, easting, northing, resolution int, err error) {
	clean := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(clean) < 5 {
		err = fmt.Errorf("mgrs: too short: %q", s)
		return
	}
	// Zone: leading digits (1–60 → 1 or 2 chars).
	idx := 0
	for idx < len(clean) && clean[idx] >= '0' && clean[idx] <= '9' {
		idx++
	}
	if idx == 0 || idx > 2 {
		err = fmt.Errorf("mgrs: bad zone in %q", s)
		return
	}
	zone, _ = strconv.Atoi(clean[:idx])
	if zone < 1 || zone > 60 {
		err = fmt.Errorf("mgrs: zone out of range: %d", zone)
		return
	}
	if idx >= len(clean) {
		err = fmt.Errorf("mgrs: missing latitude band in %q", s)
		return
	}
	latBand = clean[idx]
	idx++
	if !isLatBand(latBand) {
		err = fmt.Errorf("mgrs: bad latitude band: %c", latBand)
		return
	}
	if idx+2 > len(clean) {
		err = fmt.Errorf("mgrs: missing grid square in %q", s)
		return
	}
	gridSq = clean[idx : idx+2]
	idx += 2
	if !isColLetter(gridSq[0]) || !isRowLetter(gridSq[1]) {
		err = fmt.Errorf("mgrs: bad grid square: %s", gridSq)
		return
	}
	rest := clean[idx:]
	if len(rest)%2 != 0 {
		err = fmt.Errorf("mgrs: easting/northing must have equal digit counts (got %d)", len(rest))
		return
	}
	half := len(rest) / 2
	if half < 1 || half > 5 {
		err = fmt.Errorf("mgrs: digit count out of range: %d per side", half)
		return
	}
	easting, err = strconv.Atoi(rest[:half])
	if err != nil {
		err = fmt.Errorf("mgrs: bad easting %q: %w", rest[:half], err)
		return
	}
	northing, err = strconv.Atoi(rest[half:])
	if err != nil {
		err = fmt.Errorf("mgrs: bad northing %q: %w", rest[half:], err)
		return
	}
	mult := 1
	for i := half; i < 5; i++ {
		mult *= 10
	}
	easting *= mult
	northing *= mult
	resolution = mult // size of one grid cell in metres
	return
}

// gridToUTM converts the (zone, latBand, gridSquare, easting, northing)
// MGRS components into UTM eastings and northings (metres).
func gridToUTM(zone int, latBand byte, gridSq string, eastingPart, northingPart int) (easting, northing float64, err error) {
	colSet := colLettersForZone(zone)
	rowSet := rowLettersForZone(zone)
	colIdx := strings.IndexByte(colSet, gridSq[0])
	rowIdx := strings.IndexByte(rowSet, gridSq[1])
	if colIdx < 0 || rowIdx < 0 {
		return 0, 0, fmt.Errorf("mgrs: grid square %s not valid in zone %d", gridSq, zone)
	}
	// Column easting starts at 100000 m (the UTM zone is centred so column A = 100000).
	easting = float64((colIdx+1)*100000) + float64(eastingPart)
	// Row northing wraps every 2,000,000 m. Pick the wrap that lands the
	// final lat in the latBand range (rough check below).
	rowNorthing := float64(rowIdx) * 100000
	northing = closestNorthing(rowNorthing, latBand)
	northing += float64(northingPart)
	return easting, northing, nil
}

// utmToGridSquare returns the 100km grid square letters and the easting /
// northing modulo 100,000 metres.
func utmToGridSquare(zone int, easting, northing float64) (sq string, eastMod, northMod float64) {
	colSet := colLettersForZone(zone)
	rowSet := rowLettersForZone(zone)
	colIdx := int(easting/100000) - 1
	if colIdx < 0 {
		colIdx = 0
	}
	if colIdx >= len(colSet) {
		colIdx = len(colSet) - 1
	}
	rowIdx := int(math.Mod(northing/100000, float64(len(rowSet))))
	sq = string(colSet[colIdx]) + string(rowSet[rowIdx])
	eastMod = math.Mod(easting, 100000)
	northMod = math.Mod(northing, 100000)
	return
}

// colLettersForZone returns the 8-letter column alphabet that applies to the
// given zone. The pattern repeats every three zones starting from zone 1.
func colLettersForZone(zone int) string {
	switch ((zone - 1) % 3) {
	case 0:
		return colLetters[0:8] // A–H
	case 1:
		return colLetters[8:16] // J–R
	default:
		return colLetters[16:24] // S–Z
	}
}

// rowLettersForZone returns the row alphabet — alternates per zone parity.
func rowLettersForZone(zone int) string {
	if zone%2 == 1 {
		return rowLetters // A–V
	}
	// Even zones start at F (offset 5) for the standard "AA pattern".
	return rowLetters[5:] + rowLetters[:5]
}

// closestNorthing snaps the per-band wrap-around to the value that lands
// inside the latitude band described by latBand. We approximate: for
// southern bands C–M the northing is in [0, 10_000_000); for N–X it's in
// [0, 9_400_000) but referenced from the equator. Without a full set of
// per-band lookup tables we pick the wrap-multiple closest to the band's
// centre.
func closestNorthing(rowNorthing float64, latBand byte) float64 {
	bandCentre := bandCentreNorthing(latBand)
	wrap := 2_000_000.0
	candidates := []float64{
		rowNorthing,
		rowNorthing + wrap,
		rowNorthing + 2*wrap,
		rowNorthing + 3*wrap,
		rowNorthing + 4*wrap,
	}
	best := candidates[0]
	bestDiff := math.Abs(best - bandCentre)
	for _, c := range candidates[1:] {
		d := math.Abs(c - bandCentre)
		if d < bestDiff {
			best = c
			bestDiff = d
		}
	}
	return best
}

// bandCentreNorthing returns an approximate UTM northing at the centre of
// the given latitude band. Each 8-degree band is ~887km tall.
func bandCentreNorthing(b byte) float64 {
	bands := "CDEFGHJKLMNPQRSTUVWX" // C is southernmost (-80..-72), X is northernmost (72..84)
	idx := strings.IndexByte(bands, b)
	if idx < 0 {
		return 0
	}
	// Approximate latitude at the centre of band idx.
	lat := -80.0 + float64(idx)*8 + 4
	if b == 'X' {
		lat = 78 // X is 12 degrees tall (72..84)
	}
	if lat < 0 {
		// Southern hemisphere: UTM uses 10,000,000 - |lat-derived northing|
		return 10_000_000 - approxLatToNorthing(-lat)
	}
	return approxLatToNorthing(lat)
}

// approxLatToNorthing is a rough lat → northing mapping good enough for
// picking the right 2,000,000 m wrap. 1 degree of latitude is ~111,320 m at
// the equator; we use this constant approximation throughout.
func approxLatToNorthing(latDeg float64) float64 {
	return latDeg * 111320
}

func isNorthernHemisphere(b byte) bool {
	return b >= 'N' && b <= 'X'
}

func isLatBand(b byte) bool {
	if b == 'I' || b == 'O' {
		return false
	}
	return (b >= 'C' && b <= 'X')
}

func isColLetter(b byte) bool {
	return strings.IndexByte(colLetters, b) >= 0
}

func isRowLetter(b byte) bool {
	return strings.IndexByte(rowLetters, b) >= 0
}

// latBandLetter returns the MGRS latitude band letter for the given
// latitude in degrees.
func latBandLetter(lat float64) byte {
	bands := "CDEFGHJKLMNPQRSTUVWX"
	if lat < -80 || lat > 84 {
		return 'Z' // Antarctic / Arctic — not strictly MGRS but a marker
	}
	idx := int((lat + 80) / 8)
	if idx >= len(bands) {
		idx = len(bands) - 1
	}
	return bands[idx]
}
