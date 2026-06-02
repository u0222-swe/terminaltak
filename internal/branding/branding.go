// Package branding holds the project's static brand assets — currently the
// ASCII logo printed as a startup splash. logo.txt is co-located here so
// Go's //go:embed (which cannot cross upward package boundaries with "..")
// can reach it.
package branding

import _ "embed"

//go:embed logo.txt
var Logo string
