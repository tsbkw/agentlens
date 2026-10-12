package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tsbkw/agentlens/internal/models"
	"gopkg.in/yaml.v3"
)

// LoadedProvider wraps a ProviderDefinition with pre-compiled regular expressions.
type LoadedProvider struct {
	Definition       *models.ProviderDefinition
	SessionPathRegex *regexp.Regexp
	AuthFailureRegex []*regexp.Regexp
	MCPServerRegex   *regexp.Regexp
	ScopeRules       []CompiledScopeRule
	DeniedRegex      []*regexp.Regexp
	// TurnSkipRegex: messages whose prompt label matches are not turns (turns.skip_regex)
	TurnSkipRegex []*regexp.Regexp
}

// IsAuxiliaryTool reports whether name is one of the provider's bookkeeping tools.
func (p *LoadedProvider) IsAuxiliaryTool(name string) bool {
	for _, pattern := range p.Definition.Extraction.AuxiliaryTools {
		if pattern == name || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	return false
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

	// Pre-compile MCP server extraction regex if defined
	if def.Extraction.Fields.MCPServerRegex != "" {
		re, err := regexp.Compile(def.Extraction.Fields.MCPServerRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid mcp_server_regex %q: %w", def.Extraction.Fields.MCPServerRegex, err)
		}
		loaded.MCPServerRegex = re
	}

	// Pre-compile scope rules
	for _, rule := range def.Extraction.Scopes {
		compiled := CompiledScopeRule{ScopeRule: rule}
		if rule.NameRegex != "" {
			re, err := regexp.Compile(rule.NameRegex)
			if err != nil {
				return nil, fmt.Errorf("invalid scope name_regex %q: %w", rule.NameRegex, err)
			}
			compiled.NameRegex = re
		}
		loaded.ScopeRules = append(loaded.ScopeRules, compiled)
	}

	for _, pattern := range def.Extraction.Turns.SkipRegex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid turns.skip_regex %q: %w", pattern, err)
		}
		loaded.TurnSkipRegex = append(loaded.TurnSkipRegex, re)
	}

	for _, pattern := range def.Extraction.Results.DeniedRegex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid results.denied_regex %q: %w", pattern, err)
		}
		loaded.DeniedRegex = append(loaded.DeniedRegex, re)
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
	for i, pattern := range def.Source.PathPatterns {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return fmt.Errorf("source.path_patterns[%d]: invalid glob %q: %v", i, pattern, err)
		}
	}
	if ct := def.Source.ChildTraces; ct != nil && ct.PathGlob != "" {
		if _, err := filepath.Match(ct.PathGlob, ""); err != nil {
			return fmt.Errorf("source.child_traces.path_glob: invalid glob %q: %v", ct.PathGlob, err)
		}
	}
	switch def.Extraction.Results.Correlation {
	case "", models.CorrelationCallID:
	case models.CorrelationPreviousStep:
		if def.Extraction.Results.StepIndexField == "" {
			return fmt.Errorf("extraction.results: correlation %q needs 'step_index_field'", models.CorrelationPreviousStep)
		}
	default:
		return fmt.Errorf("extraction.results.correlation must be %q or %q, got %q", models.CorrelationCallID, models.CorrelationPreviousStep, def.Extraction.Results.Correlation)
	}
	for name, filter := range map[string]string{
		"extraction.events.tool_call_filter":      def.Extraction.Events.ToolCallFilter,
		"extraction.events.tool_call_item_filter": def.Extraction.Events.ToolCallItemFilter,
		"extraction.turns.filter":                 def.Extraction.Turns.Filter,
		"extraction.results.filter":               def.Extraction.Results.Filter,
		"extraction.results.item_filter":          def.Extraction.Results.ItemFilter,
	} {
		if err := validateFilter(filter); err != nil {
			return fmt.Errorf("%s: %v", name, err)
		}
	}
	for i, rule := range def.Extraction.Scopes {
		if rule.ToolName == "" {
			return fmt.Errorf("extraction.scopes[%d]: missing required field 'tool_name'", i)
		}
		if rule.Kind != models.ScopeKindSkill && rule.Kind != models.ScopeKindSubagent {
			return fmt.Errorf("extraction.scopes[%d]: kind must be %q or %q, got %q", i, models.ScopeKindSkill, models.ScopeKindSubagent, rule.Kind)
		}
	}
	return nil
}

// validateFilter rejects a filter clause that uses a single "=", which the evaluator would
// otherwise read as a bare field path that never matches what the author meant.
func validateFilter(filter string) error {
	for _, group := range strings.Split(filter, "||") {
		for _, clause := range strings.Split(group, "&&") {
			c := strings.TrimSpace(clause)
			stripped := strings.ReplaceAll(strings.ReplaceAll(c, "==", ""), "!=", "")
			if strings.Contains(stripped, "=") {
				return fmt.Errorf("clause %q uses a single '='; write '==' or '!='", c)
			}
		}
	}
	return nil
}
