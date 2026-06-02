package branding

import (
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/term"
)

// SplashDuration is how long PrintSplash blocks while the logo is visible.
const SplashDuration = 2 * time.Second

// PrintSplash clears the terminal, draws the embedded logo centered both
// horizontally and vertically, sleeps for SplashDuration, then returns. The
// caller is expected to take over the screen afterwards (typically via a TUI
// in alt-screen mode). If the terminal is too small to fit the logo, or fd is
// not a TTY, the splash is skipped silently.
func PrintSplash(w io.Writer, fd int) {
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return
	}

	raw := strings.Split(strings.TrimRight(Logo, "\n"), "\n")
	// Right-trim each line: source has uniform padding for editor alignment,
	// but visible width is what matters for fit and centering.
	lines := make([]string, len(raw))
	logoW := 0
	for i, l := range raw {
		lines[i] = strings.TrimRight(l, " ")
		if n := len([]rune(lines[i])); n > logoW {
			logoW = n
		}
	}
	logoH := len(lines)
	// Width is non-negotiable: if it wraps, the shape is destroyed. Height
	// is forgiving — if the terminal is shorter, the top of the logo
	// scrolls off and the user still sees the lower half (TAK banner +
	// point) which carries the brand.
	if cols < logoW {
		return
	}

	const (
		clearScreen = "\033[2J"
		cursorHome  = "\033[H"
		hideCursor  = "\033[?25l"
		showCursor  = "\033[?25h"
	)

	topPad := (rows - logoH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := strings.Repeat(" ", (cols-logoW)/2)

	var b strings.Builder
	b.WriteString(clearScreen)
	b.WriteString(cursorHome)
	b.WriteString(hideCursor)
	b.WriteString(strings.Repeat("\n", topPad))
	for _, l := range lines {
		b.WriteString(leftPad)
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprint(w, b.String())

	time.Sleep(SplashDuration)
	fmt.Fprint(w, showCursor)
}
