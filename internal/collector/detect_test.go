package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsbkw/agentlens/internal/providers"
)

func TestDetectProvider(t *testing.T) {
	builtins, err := providers.LoadBuiltinProviders()
	if err != nil {
		t.Fatalf("LoadBuiltinProviders failed: %v", err)
	}

	cases := map[string]string{
		"sample_claude_code_session.jsonl": "claude-code",
		"sample_incident_response.jsonl":   "antigravity",
	}
	for file, want := range cases {
		got := DetectProvider(filepath.Join("..", "..", "examples", "traces", file), builtins)
		if got == nil || got.Definition.Provider.ID != want {
			t.Errorf("%s: expected %q, got %v", file, want, got)
		}
	}

	unknown := filepath.Join(t.TempDir(), "unknown.jsonl")
	if err := os.WriteFile(unknown, []byte(`{"hello":"world"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DetectProvider(unknown, builtins); got != nil {
		t.Errorf("Expected no provider for an unrecognised trace, got %q", got.Definition.Provider.ID)
	}
}
