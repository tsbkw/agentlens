package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExtractProviderFlag(t *testing.T) {
	cases := []struct {
		in       []string
		wantArgs []string
		wantSpec string
	}{
		{[]string{"agentlens", "list"}, []string{"agentlens", "list"}, ""},
		{[]string{"agentlens", "--provider", "claude-code", "list"}, []string{"agentlens", "list"}, "claude-code"},
		{[]string{"agentlens", "graph", "abc", "-p", "x.yaml"}, []string{"agentlens", "graph", "abc"}, "x.yaml"},
		{[]string{"agentlens", "--provider=cursor", "trace", "s1"}, []string{"agentlens", "trace", "s1"}, "cursor"},
	}
	for _, c := range cases {
		args, spec, err := extractProviderFlag(c.in)
		if err != nil {
			t.Errorf("%v: unexpected error %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(args, c.wantArgs) || spec != c.wantSpec {
			t.Errorf("%v: got (%v, %q), want (%v, %q)", c.in, args, spec, c.wantArgs, c.wantSpec)
		}
	}

	if _, _, err := extractProviderFlag([]string{"agentlens", "list", "--provider"}); err == nil {
		t.Errorf("Expected error for --provider without a value")
	}
}

func TestRunUnknownProvider(t *testing.T) {
	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp failed: %v", err)
	}
	if err := app.Run([]string{"agentlens", "--provider", "nope", "list"}); err == nil {
		t.Errorf("Expected error for unknown provider")
	}
}

func TestParseReportOptions(t *testing.T) {
	loc := time.FixedZone("JST", 9*3600)
	now := time.Date(2026, 10, 11, 15, 30, 0, 0, loc)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, loc) }

	opts, err := ParseReportOptions(nil, now)
	if err != nil || !opts.Window.Since.Equal(day(10, 4)) || !opts.Window.Until.IsZero() || opts.Top != 10 {
		t.Errorf("Default: expected the last 7 days, got %+v (%v)", opts, err)
	}

	opts, err = ParseReportOptions([]string{"--since", "2026-10-01", "--until=2026-10-10", "--project", "repo", "--top", "3", "--json"}, now)
	if err != nil || !opts.Window.Since.Equal(day(10, 1)) || !opts.Window.Until.Equal(day(10, 11)) ||
		opts.Project != "repo" || opts.Top != 3 || !opts.JSON {
		t.Errorf("Expected --until to include its whole day, got %+v (%v)", opts, err)
	}

	opts, err = ParseReportOptions([]string{"--since", "30d"}, now)
	if err != nil || !opts.Window.Since.Equal(day(9, 11)) {
		t.Errorf("Expected 30 days ago at midnight, got %+v (%v)", opts, err)
	}

	for _, bad := range [][]string{
		{"--since", "yesterday"},
		{"--since", "2026-10-10", "--until", "2026-10-01"},
		{"--top", "0"},
		{"--since"},
		{"--verbose"},
	} {
		if _, err := ParseReportOptions(bad, now); err == nil {
			t.Errorf("Expected %v to be rejected", bad)
		}
	}
}

func TestParsePortFlagAndWithoutFlag(t *testing.T) {
	if p, err := parsePortFlag(nil); err != nil || p != 8000 {
		t.Errorf("default port: %d, %v", p, err)
	}
	if p, err := parsePortFlag([]string{"--port", "9000"}); err != nil || p != 9000 {
		t.Errorf("--port 9000: %d, %v", p, err)
	}
	if p, err := parsePortFlag([]string{"--port=9001"}); err != nil || p != 9001 {
		t.Errorf("--port=9001: %d, %v", p, err)
	}
	for _, bad := range [][]string{{"--port"}, {"--port", "x"}, {"--port", "70000"}, {"--open"}} {
		if _, err := parsePortFlag(bad); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
	rest, found := withoutFlag([]string{"--trace", "abc"}, "--trace")
	if !found || len(rest) != 1 || rest[0] != "abc" {
		t.Errorf("withoutFlag = %v, %v", rest, found)
	}
	if rest, _ := withoutFlag([]string{"--trace"}, "--trace"); len(rest) != 0 {
		t.Errorf("Expected no session ID left, got %v", rest)
	}
}

func TestHelpWorksWithABadProvider(t *testing.T) {
	app, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Run([]string{"agentlens", "-p", "nope", "help"}); err != nil {
		t.Errorf("help must not need a provider: %v", err)
	}
	if err := app.Run([]string{"agentlens", "graph", "--trace"}); err == nil || !strings.Contains(err.Error(), "missing session ID") {
		t.Errorf("Expected a missing-ID error, got %v", err)
	}
}
