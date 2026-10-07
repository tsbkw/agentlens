package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAntigravityProvider(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("Antigravity example YAML not found at relative path")
	}

	loaded, err := LoadProviderFromFile(path)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	if loaded.Definition.Provider.ID != "antigravity" {
		t.Errorf("Expected provider id 'antigravity', got %q", loaded.Definition.Provider.ID)
	}
	if loaded.SessionPathRegex == nil {
		t.Errorf("Expected compiled session path regex, got nil")
	}
	if len(loaded.AuthFailureRegex) == 0 {
		t.Errorf("Expected compiled auth failure regexes, got none")
	}
}

func TestLoadGenericJSONLProvider(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "providers", "generic_jsonl.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("Generic JSONL example YAML not found at relative path")
	}

	loaded, err := LoadProviderFromFile(path)
	if err != nil {
		t.Fatalf("Failed to load Generic JSONL provider: %v", err)
	}

	if loaded.Definition.Provider.ID != "generic-jsonl" {
		t.Errorf("Expected provider id 'generic-jsonl', got %q", loaded.Definition.Provider.ID)
	}
}

func TestValidationErrors(t *testing.T) {
	invalidYAML := []byte(`
schema_version: "1.0"
# Missing provider.id and name
source:
  type: "file"
`)

	_, err := LoadProviderFromBytes(invalidYAML)
	if err == nil {
		t.Fatalf("Expected validation error for missing fields, got nil")
	}
}
