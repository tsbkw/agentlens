// Package ansi holds the terminal colors of the CLI. They are empty strings when the output
// is not a terminal or the user turned colors off, so piped and redirected output stays plain.
package ansi

import "os"

// Color escape sequences. Each is "" when colors are disabled.
var (
	Reset   string
	Bold    string
	Dim     string
	Red     string
	Green   string
	Yellow  string
	Blue    string
	Magenta string
	Cyan    string
)

var enabled bool

func init() {
	Set(detect())
}

// Enabled reports whether colors are on.
func Enabled() bool { return enabled }

// Set turns colors on or off.
func Set(on bool) {
	enabled = on
	pick := func(code string) string {
		if on {
			return code
		}
		return ""
	}
	Reset = pick("\033[0m")
	Bold = pick("\033[1m")
	Dim = pick("\033[2m")
	Red = pick("\033[31m")
	Green = pick("\033[32m")
	Yellow = pick("\033[33m")
	Blue = pick("\033[34m")
	Magenta = pick("\033[35m")
	Cyan = pick("\033[36m")
}

// detect follows the usual conventions: NO_COLOR turns colors off, FORCE_COLOR turns them
// on, and otherwise they are on only when stdout is a terminal.
func detect() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if v, ok := os.LookupEnv("FORCE_COLOR"); ok && v != "0" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
