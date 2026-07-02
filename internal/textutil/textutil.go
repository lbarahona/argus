// Package textutil provides small string helpers shared across renderers.
package textutil

import "unicode/utf8"

// Truncate shortens s to at most max runes, appending "..." when truncated.
// Truncation happens on rune boundaries so multibyte characters are never
// split into invalid UTF-8.
func Truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
