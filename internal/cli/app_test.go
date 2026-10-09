package cli

import (
	"reflect"
	"testing"
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
