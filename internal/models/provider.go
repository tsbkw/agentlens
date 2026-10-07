package models

// ProviderDefinition represents a declarative schema configuring how AgentLens
// extracts tool call traces from a specific Generative AI system.
type ProviderDefinition struct {
	SchemaVersion string             `json:"schema_version" yaml:"schema_version"`
	Provider      ProviderInfo       `json:"provider" yaml:"provider"`
	Source        SourceConfig       `json:"source" yaml:"source"`
	Extraction    ExtractionConfig   `json:"extraction" yaml:"extraction"`
	AnomalyRules  AnomalyRulesConfig `json:"anomaly_rules,omitempty" yaml:"anomaly_rules,omitempty"`
}

// ProviderInfo provides metadata about the AI system.
type ProviderInfo struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// SourceConfig defines where and how raw log data is stored.
type SourceConfig struct {
	Type         string   `json:"type" yaml:"type"` // "file", "directory_watch", "command", "otel_collector"
	PathPatterns []string `json:"path_patterns" yaml:"path_patterns"`
	Format       string   `json:"format" yaml:"format"` // "jsonl", "json", "regex_lines"
}

// ExtractionConfig specifies how to retrieve session IDs, filter events, and map fields.
type ExtractionConfig struct {
	Session SessionExtraction `json:"session" yaml:"session"`
	Events  EventFilters      `json:"events" yaml:"events"`
	Fields  FieldMappings     `json:"fields" yaml:"fields"`
}

// SessionExtraction specifies rules to find the session or conversation ID.
type SessionExtraction struct {
	IDJSONPath string `json:"id_jsonpath,omitempty" yaml:"id_jsonpath,omitempty"`
	PathRegex  string `json:"path_regex,omitempty" yaml:"path_regex,omitempty"`
}

// EventFilters specifies criteria to detect tool calls and subagent spawns.
type EventFilters struct {
	ToolCallFilter      string `json:"tool_call_filter" yaml:"tool_call_filter"`
	SubagentSpawnFilter string `json:"subagent_spawn_filter,omitempty" yaml:"subagent_spawn_filter,omitempty"`
}

// FieldMappings maps raw trace properties to normalized CallNode fields.
type FieldMappings struct {
	CallID       string `json:"call_id" yaml:"call_id"`
	ParentID     string `json:"parent_id,omitempty" yaml:"parent_id,omitempty"`
	Timestamp    string `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	DurationMs   string `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	CallType     string `json:"call_type,omitempty" yaml:"call_type,omitempty"`
	ToolName     string `json:"tool_name" yaml:"tool_name"`
	MCPServer    string `json:"mcp_server,omitempty" yaml:"mcp_server,omitempty"`
	Arguments    string `json:"arguments,omitempty" yaml:"arguments,omitempty"`
	Status       string `json:"status,omitempty" yaml:"status,omitempty"`
	Output       string `json:"output,omitempty" yaml:"output,omitempty"`
	ErrorMessage string `json:"error_message,omitempty" yaml:"error_message,omitempty"`
}

// AnomalyRulesConfig defines platform-specific heuristics for detecting errors.
type AnomalyRulesConfig struct {
	AuthFailureRegex []string       `json:"auth_failure_regex,omitempty" yaml:"auth_failure_regex,omitempty"`
	KnownFallbacks   []FallbackRule `json:"known_fallbacks,omitempty" yaml:"known_fallbacks,omitempty"`
}

// FallbackRule defines a known substitution pattern from an intended tool to a fallback tool.
type FallbackRule struct {
	OriginalTool string `json:"original_tool" yaml:"original_tool"`
	FallbackTool string `json:"fallback_tool" yaml:"fallback_tool"`
	Description  string `json:"description,omitempty" yaml:"description,omitempty"`
}
