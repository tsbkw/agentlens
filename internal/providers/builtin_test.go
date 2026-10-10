package providers

import (
	"path/filepath"
	"testing"
)

func TestLoadBuiltinProviders(t *testing.T) {
	builtins, err := LoadBuiltinProviders()
	if err != nil {
		t.Fatalf("LoadBuiltinProviders failed: %v", err)
	}
	want := []string{"antigravity", "claude-code", "cursor", "generic-jsonl"}
	if len(builtins) != len(want) {
		t.Fatalf("Expected %d built-in providers, got %d", len(want), len(builtins))
	}
	for i, id := range want {
		if builtins[i].Definition.Provider.ID != id {
			t.Errorf("builtins[%d]: expected %q, got %q", i, id, builtins[i].Definition.Provider.ID)
		}
	}
}

func TestResolveProvider(t *testing.T) {
	builtins, err := LoadBuiltinProviders()
	if err != nil {
		t.Fatalf("LoadBuiltinProviders failed: %v", err)
	}

	p, err := ResolveProvider("claude-code", builtins)
	if err != nil || p.Definition.Provider.ID != "claude-code" {
		t.Errorf("Expected built-in claude-code, got %v (err %v)", p, err)
	}

	p, err = ResolveProvider(filepath.Join("..", "..", "examples", "providers", "cursor.yaml"), builtins)
	if err != nil || p.Definition.Provider.ID != "cursor" {
		t.Errorf("Expected provider loaded from file, got %v (err %v)", p, err)
	}

	if _, err := ResolveProvider("does-not-exist", builtins); err == nil {
		t.Errorf("Expected error for unknown provider")
	}
}
