// Package textutil holds small text helpers shared by the visualizers.
package textutil

import (
	"fmt"
	"time"
)

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

// TruncateHead keeps the last max characters of s and prepends prefix when anything was cut.
// It suits paths and project names, whose distinguishing part is the end.
func TruncateHead(s string, max int, prefix string) string {
	if max < 0 {
		max = 0
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return prefix + string(runes[len(runes)-max:])
}

// FormatDuration renders a duration in milliseconds compactly: 850ms, 2.3s, 1m05s.
func FormatDuration(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%dm%02ds", ms/60_000, (ms%60_000)/1000)
	}
}

// FormatAge renders how long ago something happened, coarsely: 45m, 5h, 3d, 6w.
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 21*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
}

// FormatSize renders a byte count as KB or MB.
func FormatSize(bytes int64) string {
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
