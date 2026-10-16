package domain

import (
	"regexp"
	"strings"
)

// DefaultMarker is run-fleet's closing line once its tags are removed.
const DefaultMarker = "🤖 unattendish agent"

var (
	htmlTag  = regexp.MustCompile(`<[^>]*>`)
	emphasis = regexp.MustCompile("[*_~`]+")
)

// normalizeLine removes HTML tags and Markdown emphasis and trims space.
func normalizeLine(s string) string {
	s = htmlTag.ReplaceAllString(s, "")
	s = emphasis.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// EndsWithMarker says whether body's last non-empty line, normalized, is
// the marker: the comment is an echo of the holding session's own side.
func EndsWithMarker(body, marker string) bool {
	want := normalizeLine(marker)
	if want == "" {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := normalizeLine(lines[i]); line != "" {
			return line == want
		}
	}
	return false
}
