// Package safetext neutralises untrusted strings before they are rendered to
// the terminal. CoT callsigns, chat text, team-colour labels and similar
// fields originate from other TAK clients (relayed through the server) and are
// therefore attacker-controllable. Rendering them verbatim would allow ANSI /
// OSC escape-sequence injection: redrawing the UI, spoofing or hiding
// messages, setting the window title, or writing to the user's clipboard via
// OSC 52.
//
// Clean strips every C0 control character (0x00–0x1F, including ESC 0x1B),
// DEL (0x7F) and every C1 control character (0x80–0x9F). Removing the escape
// introducers (ESC, and the C1 CSI/OSC bytes 0x9B/0x9D) is sufficient to
// neutralise an entire escape sequence — whatever literal text remains
// ("[31m", "]0;title") is harmless. All printable Unicode is preserved.
package safetext

import "strings"

// Clean returns s with all control characters removed. It is safe to call on
// any network-derived string immediately before storing or rendering it.
func Clean(s string) string {
	if s == "" {
		return s
	}
	// Fast path: nothing to strip.
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isControl reports whether r is a C0, DEL, or C1 control character.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}
