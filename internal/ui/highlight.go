package ui

import (
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
)

// highlightYAML colors YAML with chroma, or returns it plain when color is off.
func highlightYAML(src string, color bool) string {
	if !color || src == "" {
		return strings.TrimRight(src, "\n")
	}
	var b strings.Builder
	if err := quick.Highlight(&b, src, "yaml", "terminal256", "monokai"); err != nil {
		return strings.TrimRight(src, "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
