package models

import (
	"time"
)

// CallNodeType classifies invocation elements.
type CallNodeType string

const (
	NodeTypeUserTurn   CallNodeType = "user_turn"
	NodeTypeAgent      CallNodeType = "agent"
	NodeTypeSubagent   CallNodeType = "subagent"
	NodeTypeSkill      CallNodeType = "skill"
	NodeTypeMCPTool    CallNodeType = "mcp_tool"
	NodeTypeSystemTool CallNodeType = "system_tool"
)

// CallStatus represents the execution outcome of a call.
type CallStatus string

const (
	StatusSuccess CallStatus = "success"
	StatusFailed  CallStatus = "failed"
	StatusTimeout CallStatus = "timeout"
	// StatusDenied: the call never ran because the user or a permission policy refused it.
	// It is not a tool failure, so it never starts a fallback or a retry loop.
	StatusDenied  CallStatus = "denied"
	StatusUnknown CallStatus = "unknown"
)

// AnomalyType identifies the category of detected irregularity.
type AnomalyType string

const (
	AnomalySilentFallback AnomalyType = "silent_fallback"
	AnomalyAuthExpired    AnomalyType = "auth_expired"
	AnomalyRetryLoop      AnomalyType = "retry_loop"
)

// AnomalySeverity indicates how critical the issue is.
type AnomalySeverity string

const (
	SeverityInfo     AnomalySeverity = "info"
	SeverityWarning  AnomalySeverity = "warning"
	SeverityCritical AnomalySeverity = "critical"
)

// AnomalyRecord details an anomaly attached to a node or edge.
type AnomalyRecord struct {
	ID             string          `json:"id"`
	Type           AnomalyType     `json:"type"`
	Severity       AnomalySeverity `json:"severity"`
	NodeID         string          `json:"node_id"`
	RelatedNodeID  string          `json:"related_node_id,omitempty"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Recommendation string          `json:"recommendation,omitempty"`
	// Confidence and Evidence qualify heuristic findings such as silent fallbacks.
	Confidence Confidence `json:"confidence,omitempty"`
	Evidence   string     `json:"evidence,omitempty"`
}

// Confidence rates how strongly the trace supports a heuristic finding.
type Confidence string

const (
	// ConfidenceHigh: the trace shows concrete evidence (same service, CLI or identifiers).
	ConfidenceHigh Confidence = "high"
	// ConfidenceLow: only the order of calls suggests it; treat it as a lead to check.
	ConfidenceLow Confidence = "low"
)

// CallNode represents a single vertex in the execution graph.
type CallNode struct {
	ID           string                 `json:"id"`
	SessionID    string                 `json:"session_id"`
	ParentID     string                 `json:"parent_id,omitempty"`
	TurnIndex    int                    `json:"turn_index"`
	CallerScope  string                 `json:"caller_scope,omitempty"` // "Agent", "Skill: <name>", "Subagent: <name>"
	ScopeName    string                 `json:"scope_name,omitempty"`   // Scope this call opens, e.g. "Skill: <name>"
	Type         CallNodeType           `json:"type"`
	Name         string                 `json:"name"`
	MCPServer    string                 `json:"mcp_server,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
	DurationMs   int64                  `json:"duration_ms,omitempty"`
	Status       CallStatus             `json:"status"`
	Arguments    map[string]interface{} `json:"arguments,omitempty"`
	Output       interface{}            `json:"output,omitempty"`
	ErrorMessage string                 `json:"error_message,omitempty"`
	Anomalies    []AnomalyRecord        `json:"anomalies,omitempty"`
	// ParentLink records how a call from a child (subagent) trace was linked to its spawner.
	// Empty for calls of the main trace.
	ParentLink ParentLink `json:"parent_link,omitempty"`
	// Auxiliary marks agent bookkeeping (tool discovery, planning, task tracking, questions to
	// the user). Such calls never do the user's task, so they are neither a substitute for a
	// failed call nor a break in a sequence of attempts.
	Auxiliary bool `json:"auxiliary,omitempty"`
	// Synthetic marks nodes the graph builder adds for structure (user turns, Skill and
	// Subagent scopes). They are not calls and take no part in detection or call metrics.
	Synthetic bool `json:"synthetic,omitempty"`
}

// IsInferredPlaceholder reports whether the node stands in for an unrecorded spawning call.
func (n *CallNode) IsInferredPlaceholder() bool {
	return n.ParentLink == ParentLinkInferred && n.ParentID == ""
}

// ParentLink describes the evidence that attributes a child trace to its spawning call.
type ParentLink string

const (
	// ParentLinkSidecar: the child trace's metadata names the spawning call ID.
	ParentLinkSidecar ParentLink = "sidecar"
	// ParentLinkResult: the spawning call's result names the child trace.
	ParentLinkResult ParentLink = "result"
	// ParentLinkInferred: no record links the child trace to a call. Its calls are placed
	// under a placeholder subagent node in the turn active when the child trace started,
	// which is a best guess rather than recorded fact.
	ParentLinkInferred ParentLink = "inferred"
)

// ExecutionTurn represents a conversational turn initiated by a user prompt.
type ExecutionTurn struct {
	Index     int        `json:"index"`
	Prompt    string     `json:"prompt"`
	Timestamp time.Time  `json:"timestamp"`
	Nodes     []CallNode `json:"nodes"`
}

// CallerDependency summarizes who calls what across the session.
type CallerDependency struct {
	Caller       string       `json:"caller"`      // e.g. "Agent", "Skill: quota-aware-task-runner"
	CallerType   CallNodeType `json:"caller_type"` // agent, skill, subagent
	Callee       string       `json:"callee"`      // e.g. "mcp_github_create_issue", "run_command"
	CalleeType   CallNodeType `json:"callee_type"` // mcp_tool, skill, system_tool
	MCPServer    string       `json:"mcp_server,omitempty"`
	CallCount    int          `json:"call_count"`
	SuccessCount int          `json:"success_count"`
	FailCount    int          `json:"fail_count"` // failed or timed out
	DeniedCount  int          `json:"denied_count,omitempty"`
	IsFallback   bool         `json:"is_fallback"`
	FallbackTo   string       `json:"fallback_to,omitempty"`
	// FallbackConfidence qualifies IsFallback ("high" or "low").
	FallbackConfidence Confidence `json:"fallback_confidence,omitempty"`
	// Inferred marks a caller -> callee relation that no record states, e.g. a subagent whose
	// spawning call is not recorded and was placed under the agent by time.
	Inferred bool               `json:"inferred,omitempty"`
	Children []CallerDependency `json:"children,omitempty"`
}

// CallEdgeType represents edge semantics in the DAG.
type CallEdgeType string

const (
	EdgeTypeCalls      CallEdgeType = "calls"
	EdgeTypeSpawns     CallEdgeType = "spawns"
	EdgeTypeFallbackTo CallEdgeType = "fallback_to"
	EdgeTypeRetries    CallEdgeType = "retries"
)

// CallEdge represents a directed relationship between two nodes.
type CallEdge struct {
	SourceID string       `json:"source_id"`
	TargetID string       `json:"target_id"`
	Type     CallEdgeType `json:"type"`
}

// ExecutionSession groups all nodes and edges for a conversation.
type ExecutionSession struct {
	SessionID    string             `json:"session_id"`
	Provider     string             `json:"provider"`
	StartTime    time.Time          `json:"start_time"`
	EndTime      time.Time          `json:"end_time"`
	Turns        []ExecutionTurn    `json:"turns,omitempty"`
	Nodes        []CallNode         `json:"nodes"`
	Edges        []CallEdge         `json:"edges"`
	Dependencies []CallerDependency `json:"dependencies,omitempty"`
	Anomalies    []AnomalyRecord    `json:"anomalies,omitempty"`
}
