package safetext

import "testing"

func TestClean(t *testing.T) {
	// c1 is a properly UTF-8-encoded C1 control rune (U+009B, the single-byte
	// CSI introducer). Network text reaches us already UTF-8-decoded by the
	// XML parser, so C1 controls arrive as real runes like this rather than
	// as raw 0x9B bytes.
	const c1 = ""
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "Alpha-1", "Alpha-1"},
		{"unicode preserved", "Ödläggåre 北京", "Ödläggåre 北京"},
		{"strips ESC CSI", "red\x1b[31mtext\x1b[0m", "red[31mtext[0m"},
		{"strips OSC 52 clipboard write", "x\x1b]52;c;ZXZpbA==\x07y", "x]52;c;ZXZpbA==y"},
		{"strips bare ESC", "a\x1bb", "ab"},
		{"strips C0 controls and newline", "line1\nline2\ttab\r", "line1line2tab"},
		{"strips DEL", "a\x7fb", "ab"},
		{"strips C1 introducer", "a" + c1 + "b", "ab"},
		{"strips NUL", "a\x00b", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Clean(tt.in); got != tt.want {
				t.Errorf("Clean(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
