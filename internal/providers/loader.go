package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/tsbkw/agentlens/internal/models"
	"gopkg.in/yaml.v3"
)

// LoadedProvider wraps a ProviderDefinition with pre-compiled regular expressions.
type LoadedProvider struct {
	Definition        *models.ProviderDefinition
	SessionPathRegex  *regexp.Regexp
	AuthFailureRegex  []*regexp.Regexp
}

// LoadProviderFromFile reads and parses a YAML provider definition from disk.
func LoadProviderFromFile(path string) (*LoadedProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read provider file %q: %w", path, err)
	}
	return LoadProviderFromBytes(data)
}

// LoadProviderFromBytes deserializes and validates a provider definition from byte content.
func LoadProviderFromBytes(data []byte) (*LoadedProvider, error) {
	var def models.ProviderDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("failed to parse provider YAML: %w", err)
	}

	if err := validateDefinition(&def); err != nil {
		return nil, fmt.Errorf("invalid provider definition: %w", err)
	}

	loaded := &LoadedProvider{
		Definition: &def,
	}

	// Pre-compile session path regex if defined
	if def.Extraction.Session.PathRegex != "" {
		re, err := regexp.Compile(def.Extraction.Session.PathRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid session path_regex %q: %w", def.Extraction.Session.PathRegex, err)
		}
		loaded.SessionPathRegex = re
	}

	// Pre-compile anomaly auth failure regex patterns
	for _, pattern := range def.AnomalyRules.AuthFailureRegex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid auth_failure_regex %q: %w", pattern, err)
		}
		loaded.AuthFailureRegex = append(loaded.AuthFailureRegex, re)
	}

	return loaded, nil
}

// LoadAllProvidersFromDirectory discovers and loads all .yaml / .yml provider files in a folder.
func LoadAllProvidersFromDirectory(dir string) ([]*LoadedProvider, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read provider directory %q: %w", dir, err)
	}

	var results []*LoadedProvider
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		provider, err := LoadProviderFromFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("error loading %s: %w", fullPath, err)
		}
		results = append(results, provider)
	}

	return results, nil
}

func validateDefinition(def *models.ProviderDefinition) error {
	if def.SchemaVersion == "" {
		return fmt.Errorf("missing required field 'schema_version'")
	}
	if def.Provider.ID == "" {
		return fmt.Errorf("missing required field 'provider.id'")
	}
	if def.Provider.Name == "" {
		return fmt.Errorf("missing required field 'provider.name'")
	}
	if def.Source.Type == "" {
		return fmt.Errorf("missing required field 'source.type'")
	}
	if def.Extraction.Events.ToolCallFilter == "" {
		return fmt.Errorf("missing required field 'extraction.events.tool_call_filter'")
	}
	if def.Extraction.Fields.CallID == "" {
		return fmt.Errorf("missing required field 'extraction.fields.call_id'")
	}
	if def.Extraction.Fields.ToolName == "" {
		return fmt.Errorf("missing required field 'extraction.fields.tool_name'")
	}
	return nil
}
