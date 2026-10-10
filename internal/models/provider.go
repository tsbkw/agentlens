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
	Turns   TurnExtraction    `json:"turns,omitempty" yaml:"turns,omitempty"`
	Results ResultExtraction  `json:"results,omitempty" yaml:"results,omitempty"`
	Scopes  []ScopeRule       `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// Scope kinds a ScopeRule can open.
const (
	ScopeKindSkill    = "skill"
	ScopeKindSubagent = "subagent"
)

// ScopeRule declares a tool call that activates a Skill or spawns a Subagent.
type ScopeRule struct {
	// ToolName is the exact tool name, or a prefix pattern ending in "*" (e.g. "skill_*").
	ToolName string `json:"tool_name" yaml:"tool_name"`
	// Kind is "skill" or "subagent".
	Kind string `json:"kind" yaml:"kind"`
	// NameField is the dot-path into the call arguments holding the scope name, with "||"
	// alternatives (e.g. "Role || TypeName"). Empty uses the tool name itself.
	NameField string `json:"name_field,omitempty" yaml:"name_field,omitempty"`
	// NameRegex optionally extracts the name via its first capture group; when set,
	// the rule only applies if it matches (e.g. "/skills/([^/]+)/SKILL\\.md$").
	NameRegex string `json:"name_regex,omitempty" yaml:"name_regex,omitempty"`
	// EnterScope controls whether subsequent calls in the turn are attributed to the new
	// scope (default true). Use false when the spawned scope logs its calls elsewhere.
	EnterScope *bool `json:"enter_scope,omitempty" yaml:"enter_scope,omitempty"`
}

// TurnExtraction specifies how user prompts that start a new conversational turn are detected.
type TurnExtraction struct {
	// Filter identifies events that carry a user prompt (e.g. "type == 'USER_INPUT'").
	Filter string `json:"filter,omitempty" yaml:"filter,omitempty"`
	// PromptField is the dot-path of the prompt. Arrays of content blocks are reduced to their text.
	PromptField string `json:"prompt_field,omitempty" yaml:"prompt_field,omitempty"`
	// TimestampField is the dot-path of the prompt timestamp (RFC 3339).
	TimestampField string `json:"timestamp_field,omitempty" yaml:"timestamp_field,omitempty"`
	// PromptTag optionally names a wrapper tag (e.g. "USER_REQUEST") whose inner text is the prompt.
	PromptTag string `json:"prompt_tag,omitempty" yaml:"prompt_tag,omitempty"`
}

// Result correlation strategies.
const (
	CorrelationCallID       = "call_id"
	CorrelationPreviousStep = "previous_step"
)

// ResultExtraction specifies how tool results are attached back to their tool calls.
type ResultExtraction struct {
	// Correlation is "call_id" (results reference the call ID) or "previous_step"
	// (the event at step N holds the result of the call made at step N-1).
	Correlation string `json:"correlation,omitempty" yaml:"correlation,omitempty"`
	// Filter identifies events that carry tool results. Empty matches every event.
	Filter string `json:"filter,omitempty" yaml:"filter,omitempty"`
	// ItemsPath is the dot-path of an array of result items inside the event (call_id mode).
	// Empty treats the event itself as a single result.
	ItemsPath string `json:"items_path,omitempty" yaml:"items_path,omitempty"`
	// ItemFilter selects result items within ItemsPath (e.g. "type == 'tool_result'").
	ItemFilter string `json:"item_filter,omitempty" yaml:"item_filter,omitempty"`
	// CallIDField is the dot-path of the referenced call ID (call_id mode).
	CallIDField string `json:"call_id_field,omitempty" yaml:"call_id_field,omitempty"`
	// StepIndexField is the dot-path of the numeric step index (previous_step mode).
	StepIndexField string `json:"step_index_field,omitempty" yaml:"step_index_field,omitempty"`
	// OutputField is the dot-path of the result payload. Arrays of content blocks are reduced to their text.
	OutputField string `json:"output_field,omitempty" yaml:"output_field,omitempty"`
	// ErrorFlagField is the dot-path of a boolean that is true when the call failed.
	ErrorFlagField string `json:"error_flag_field,omitempty" yaml:"error_flag_field,omitempty"`
	// StatusField is the dot-path of a status string; "error"/"failed" marks the call as failed.
	StatusField string `json:"status_field,omitempty" yaml:"status_field,omitempty"`
	// TimestampField is the dot-path of the result timestamp, used to derive call duration.
	TimestampField string `json:"timestamp_field,omitempty" yaml:"timestamp_field,omitempty"`
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
	// ToolCallsPath is the dot-path of the array holding individual tool calls inside a
	// matched event (e.g. "tool_calls" or "message.content"). Defaults to "tool_calls".
	ToolCallsPath string `json:"tool_calls_path,omitempty" yaml:"tool_calls_path,omitempty"`
	// ToolCallItemFilter selects which elements of ToolCallsPath are tool calls
	// (e.g. "type == 'tool_use'"). Empty means every element is a tool call.
	ToolCallItemFilter string `json:"tool_call_item_filter,omitempty" yaml:"tool_call_item_filter,omitempty"`
}

// FieldMappings maps raw trace properties to normalized CallNode fields.
type FieldMappings struct {
	CallID     string `json:"call_id" yaml:"call_id"`
	ParentID   string `json:"parent_id,omitempty" yaml:"parent_id,omitempty"`
	Timestamp  string `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	DurationMs string `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	CallType   string `json:"call_type,omitempty" yaml:"call_type,omitempty"`
	ToolName   string `json:"tool_name" yaml:"tool_name"`
	MCPServer  string `json:"mcp_server,omitempty" yaml:"mcp_server,omitempty"`
	// MCPServerRegex extracts the MCP server name from the tool name via its first capture
	// group (e.g. "^mcp__(.+?)__"). A match also classifies the call as an MCP tool.
	MCPServerRegex string `json:"mcp_server_regex,omitempty" yaml:"mcp_server_regex,omitempty"`
	Arguments      string `json:"arguments,omitempty" yaml:"arguments,omitempty"`
	Status         string `json:"status,omitempty" yaml:"status,omitempty"`
	Output         string `json:"output,omitempty" yaml:"output,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty" yaml:"error_message,omitempty"`
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
