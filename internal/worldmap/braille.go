package worldmap

// Braille rendering uses Unicode pattern characters U+2800–U+28FF. Each
// character represents an 8-dot 2-column × 4-row sub-pixel grid, so a
// terminal canvas of W×H cells holds an effective W*2 × H*4 dot grid.
//
// Bit layout (Unicode standard):
//
//	col 0   col 1
//	  0       3      ← row 0
//	  1       4      ← row 1
//	  2       5      ← row 2
//	  6       7      ← row 3
//
// brailleDotMask[col][row] returns the bit to OR into U+2800 to light up
// the dot at (col, row) inside one Braille character.
var brailleDotMask = [2][4]rune{
	{0x01, 0x02, 0x04, 0x40}, // col 0
	{0x08, 0x10, 0x20, 0x80}, // col 1
}

// brailleBase is the base codepoint for the Unicode Braille block.
const brailleBase = 0x2800

// renderBrailleBasemap returns a width×height canvas of runes with each
// cell containing the Braille glyph that summarises whether each of its 8
// sub-cell dots is on land. Cells with no land remain a regular space —
// this keeps the basemap visually quiet so contact markers stand out.
//
// Compared to per-cell point-in-polygon (the previous approach), this
// raises the apparent rendering resolution by 8× without changing the
// outer canvas size.
func renderBrailleBasemap(viewport Viewport, width, height int) [][]rune {
	canvas := make([][]rune, height)
	for i := range canvas {
		canvas[i] = make([]rune, width)
		for j := range canvas[i] {
			canvas[i][j] = ' '
		}
	}
	if width <= 0 || height <= 0 {
		return canvas
	}

	cellLonSpan := (viewport.MaxLon - viewport.MinLon) / float64(width)
	cellLatSpan := (viewport.MaxLat - viewport.MinLat) / float64(height)

	for row := 0; row < height; row++ {
		// y direction: dot 0 is the topmost (highest lat), dot 3 is bottom.
		topLat := viewport.MaxLat - float64(row)*cellLatSpan
		for col := 0; col < width; col++ {
			leftLon := viewport.MinLon + float64(col)*cellLonSpan
			var bits rune
			for dx := 0; dx < 2; dx++ {
				// Each dot is centred at half-sub-cell offsets.
				lon := leftLon + (float64(dx)+0.5)*(cellLonSpan/2)
				for dy := 0; dy < 4; dy++ {
					lat := topLat - (float64(dy)+0.5)*(cellLatSpan/4)
					if pointInLand(lat, lon) {
						bits |= brailleDotMask[dx][dy]
					}
				}
			}
			if bits != 0 {
				canvas[row][col] = brailleBase + bits
			}
		}
	}
	return canvas
}
