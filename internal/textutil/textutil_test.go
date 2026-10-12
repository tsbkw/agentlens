package textutil

import (
	"testing"
	"time"
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

func TestTruncateHead(t *testing.T) {
	if got := TruncateHead("-Users-dev-projects-widgets", 8, "…"); got != "…-widgets" {
		t.Errorf("TruncateHead = %q", got)
	}
	if got := TruncateHead("short", 8, "…"); got != "short" {
		t.Errorf("TruncateHead must keep short strings, got %q", got)
	}
	if got := TruncateHead("日本語のパス", 3, "…"); got != "…のパス" {
		t.Errorf("TruncateHead must cut on runes, got %q", got)
	}
}

func TestFormatDurationAndSize(t *testing.T) {
	for ms, want := range map[int64]string{850: "850ms", 2300: "2.3s", 65000: "1m05s"} {
		if got := FormatDuration(ms); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", ms, got, want)
		}
	}
	if got := FormatSize(512 * 1024); got != "512.0 KB" {
		t.Errorf("FormatSize = %q", got)
	}
	if got := FormatSize(3 * 1024 * 1024); got != "3.0 MB" {
		t.Errorf("FormatSize = %q", got)
	}
}

func TestFormatAge(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "<1m", 45 * time.Minute: "45m", 5 * time.Hour: "5h", 47 * time.Hour: "47h",
		3 * 24 * time.Hour: "3d", 20 * 24 * time.Hour: "20d", 6 * 7 * 24 * time.Hour: "6w",
	}
	for d, want := range cases {
		if got := FormatAge(d); got != want {
			t.Errorf("FormatAge(%v) = %q, want %q", d, got, want)
		}
	}
}
