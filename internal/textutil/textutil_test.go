package textutil

import (
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"abcdefghijk", 10, "abcdefghij..."},
		{"このOSSへContributeしてほしいです", 8, "このOSSへCo..."},
		{"絵文字👍を含む", 4, "絵文字👍..."},
		{"", 3, ""},
		{"abc", 0, "..."},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.max, "...")
		if got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(%q, %d) produced invalid UTF-8 %q", c.in, c.max, got)
		}
	}
}
