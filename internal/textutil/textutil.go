// Package textutil holds small text helpers shared by the visualizers.
package textutil

// Truncate shortens s to at most max characters (runes, not bytes) and appends suffix when
// anything was cut, so multi-byte text such as Japanese is never split mid-character.
func Truncate(s string, max int, suffix string) string {
	if max < 0 {
		max = 0
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + suffix
		}
		count++
	}
	return s
}
