package providers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsbkw/agentlens/internal/models"
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

func TestLoadClaudeCodeProvider(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "providers", "claude_code.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("Claude Code example YAML not found at relative path")
	}

	loaded, err := LoadProviderFromFile(path)
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}

	if loaded.Definition.Provider.ID != "claude-code" {
		t.Errorf("Expected provider id 'claude-code', got %q", loaded.Definition.Provider.ID)
	}
}

func TestLoadCursorProvider(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "providers", "cursor.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("Cursor example YAML not found at relative path")
	}

	loaded, err := LoadProviderFromFile(path)
	if err != nil {
		t.Fatalf("Failed to load Cursor provider: %v", err)
	}

	if loaded.Definition.Provider.ID != "cursor" {
		t.Errorf("Expected provider id 'cursor', got %q", loaded.Definition.Provider.ID)
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

func TestValidateDefinitionRejectsBadGlobsFiltersAndCorrelation(t *testing.T) {
	base := func() *models.ProviderDefinition {
		return &models.ProviderDefinition{
			SchemaVersion: "1.0",
			Provider:      models.ProviderInfo{ID: "x", Name: "X"},
			Source:        models.SourceConfig{Type: "file", PathPatterns: []string{"~/logs/*.jsonl"}},
			Extraction: models.ExtractionConfig{
				Events: models.EventFilters{ToolCallFilter: "type == 'tool_call'"},
				Fields: models.FieldMappings{CallID: "id", ToolName: "name"},
			},
		}
	}
	if err := validateDefinition(base()); err != nil {
		t.Fatalf("A valid definition must pass: %v", err)
	}
	bad := map[string]func(d *models.ProviderDefinition){
		"glob": func(d *models.ProviderDefinition) { d.Source.PathPatterns = []string{"logs/[*.jsonl"} },
		"child glob": func(d *models.ProviderDefinition) {
			d.Source.ChildTraces = &models.ChildTraceConfig{PathGlob: "{dir}/[x"}
		},
		"single =":    func(d *models.ProviderDefinition) { d.Extraction.Events.ToolCallFilter = "type = 'tool_call'" },
		"correlation": func(d *models.ProviderDefinition) { d.Extraction.Results.Correlation = "by_magic" },
		"no step idx": func(d *models.ProviderDefinition) { d.Extraction.Results.Correlation = models.CorrelationPreviousStep },
	}
	for name, mutate := range bad {
		d := base()
		mutate(d)
		if err := validateDefinition(d); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}
