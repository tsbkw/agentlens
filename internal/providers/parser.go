package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
)

// TraceParser extracts normalized CallNodes and SessionIDs from raw log events.
type TraceParser struct {
	Provider *LoadedProvider
}

// NewTraceParser creates a TraceParser for the specified provider.
func NewTraceParser(provider *LoadedProvider) *TraceParser {
	return &TraceParser{
		Provider: provider,
	}
}

// ExtractSessionID extracts the conversation or session ID from file path or event payload.
func (p *TraceParser) ExtractSessionID(filePath string, payload map[string]interface{}) string {
	// 1. Try regex on file path
	if p.Provider.SessionPathRegex != nil && filePath != "" {
		matches := p.Provider.SessionPathRegex.FindStringSubmatch(filePath)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	// 2. Try JSON field extraction
	jsonPath := p.Provider.Definition.Extraction.Session.IDJSONPath
	if jsonPath != "" && payload != nil {
		if val := ResolveField(payload, jsonPath); val != nil {
			return fmt.Sprintf("%v", val)
		}
	}

	return ""
}

// IsToolCallEvent checks if a payload matches the provider's tool call filter.
func (p *TraceParser) IsToolCallEvent(payload map[string]interface{}) bool {
	filter := p.Provider.Definition.Extraction.Events.ToolCallFilter
	if filter == "" {
		return true
	}
	return evaluateSimpleFilter(payload, filter)
}

// ParseNodes extracts CallNodes from a raw trace event.
func (p *TraceParser) ParseNodes(sessionID string, payload map[string]interface{}, lineTimestamp time.Time) ([]models.CallNode, error) {
	if !p.IsToolCallEvent(payload) {
		return nil, nil
	}

	// Check if the payload contains multiple tool calls (e.g. Antigravity / OpenAI "tool_calls" array)
	var rawCalls []map[string]interface{}
	if rawArray, ok := payload["tool_calls"].([]interface{}); ok {
		for _, item := range rawArray {
			if callMap, ok := item.(map[string]interface{}); ok {
				rawCalls = append(rawCalls, callMap)
			}
		}
	}

	// If no tool_calls array, treat the payload itself as a single call event
	if len(rawCalls) == 0 {
		rawCalls = append(rawCalls, payload)
	}

	var nodes []models.CallNode
	for idx, callMap := range rawCalls {
		node, err := p.buildNode(sessionID, payload, callMap, idx, lineTimestamp)
		if err != nil {
			return nil, err
		}
		if node != nil {
			nodes = append(nodes, *node)
		}
	}

	return nodes, nil
}

func (p *TraceParser) buildNode(
	sessionID string,
	rootPayload map[string]interface{},
	callPayload map[string]interface{},
	index int,
	fallbackTime time.Time,
) (*models.CallNode, error) {
	fields := p.Provider.Definition.Extraction.Fields

	// Tool Name
	toolName := resolveString(callPayload, rootPayload, fields.ToolName)
	if toolName == "" {
		return nil, nil
	}

	// Call ID
	callID := resolveString(callPayload, rootPayload, fields.CallID)
	if callID == "" {
		callID = fmt.Sprintf("call-%d-%d", fallbackTime.UnixNano(), index)
	}

	// Parent ID
	parentID := resolveString(callPayload, rootPayload, fields.ParentID)

	// Tool Type Classification
	nodeType := classifyToolType(toolName, resolveString(callPayload, rootPayload, fields.CallType))

	// Timestamp
	nodeTime := fallbackTime
	if tsStr := resolveString(callPayload, rootPayload, fields.Timestamp); tsStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			nodeTime = t
		} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
			nodeTime = t
		}
	}

	// Status
	status := models.StatusSuccess
	if s := strings.ToLower(resolveString(callPayload, rootPayload, fields.Status)); s != "" {
		switch s {
		case "error", "failed", "err":
			status = models.StatusFailed
		case "timeout":
			status = models.StatusTimeout
		case "done", "success", "ok":
			status = models.StatusSuccess
		}
	}

	// Error message
	errMsg := resolveString(callPayload, rootPayload, fields.ErrorMessage)

	// Arguments
	args := resolveMap(callPayload, rootPayload, fields.Arguments)

	// Output
	var output interface{}
	if fields.Output != "" {
		output = resolveValue(callPayload, rootPayload, fields.Output)
	}

	// MCP Server extraction
	mcpServer := resolveString(callPayload, rootPayload, fields.MCPServer)
	if mcpServer == "" && strings.HasPrefix(toolName, "mcp_") {
		parts := strings.Split(toolName, "_")
		if len(parts) >= 2 {
			mcpServer = parts[1]
		}
	}

	node := &models.CallNode{
		ID:           callID,
		SessionID:    sessionID,
		ParentID:     parentID,
		Type:         nodeType,
		Name:         toolName,
		MCPServer:    mcpServer,
		Timestamp:    nodeTime,
		Status:       status,
		Arguments:    args,
		Output:       output,
		ErrorMessage: errMsg,
	}

	return node, nil
}

func classifyToolType(name string, explicitType string) models.CallNodeType {
	if explicitType != "" {
		switch strings.ToLower(explicitType) {
		case "skill":
			return models.NodeTypeSkill
		case "mcp", "mcp_tool":
			return models.NodeTypeMCPTool
		case "subagent":
			return models.NodeTypeSubagent
		case "system_tool":
			return models.NodeTypeSystemTool
		}
	}

	// Heuristics based on name
	if strings.Contains(name, "subagent") {
		return models.NodeTypeSubagent
	}
	if strings.HasPrefix(name, "mcp_") || strings.Contains(name, "_mcp") {
		return models.NodeTypeMCPTool
	}
	if strings.HasPrefix(name, "skill_") {
		return models.NodeTypeSkill
	}
	return models.NodeTypeSystemTool
}

func resolveString(callMap, rootMap map[string]interface{}, expr string) string {
	if expr == "" {
		return ""
	}
	parts := strings.Split(expr, "||")
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "null" {
			continue
		}
		if val := ResolveField(callMap, key); val != nil {
			return fmt.Sprintf("%v", val)
		}
		if strings.HasPrefix(key, "tool_call.") {
			subKey := strings.TrimPrefix(key, "tool_call.")
			if val := ResolveField(callMap, subKey); val != nil {
				return fmt.Sprintf("%v", val)
			}
		}
		if val := ResolveField(rootMap, key); val != nil {
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

func resolveMap(callMap, rootMap map[string]interface{}, expr string) map[string]interface{} {
	if expr == "" {
		return make(map[string]interface{})
	}
	parts := strings.Split(expr, "||")
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "null" {
			continue
		}
		val := ResolveField(callMap, key)
		if val == nil && strings.HasPrefix(key, "tool_call.") {
			val = ResolveField(callMap, strings.TrimPrefix(key, "tool_call."))
		}
		if val == nil {
			val = ResolveField(rootMap, key)
		}
		if val != nil {
			if m, ok := val.(map[string]interface{}); ok {
				return m
			}
			if str, ok := val.(string); ok && strings.HasPrefix(strings.TrimSpace(str), "{") {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(str), &parsed); err == nil {
					return parsed
				}
			}
		}
	}
	return make(map[string]interface{})
}

func resolveValue(callMap, rootMap map[string]interface{}, expr string) interface{} {
	parts := strings.Split(expr, "||")
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "null" {
			continue
		}
		if val := ResolveField(callMap, key); val != nil {
			return val
		}
		if strings.HasPrefix(key, "tool_call.") {
			if val := ResolveField(callMap, strings.TrimPrefix(key, "tool_call.")); val != nil {
				return val
			}
		}
		if val := ResolveField(rootMap, key); val != nil {
			return val
		}
	}
	return nil
}

// ResolveField traverses a nested map via dot-notation (e.g. "tool_call.name").
func ResolveField(data map[string]interface{}, path string) interface{} {
	if data == nil || path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var current interface{} = data

	for _, part := range parts {
		currMap, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = currMap[part]
		if current == nil {
			return nil
		}
	}
	return current
}

func evaluateSimpleFilter(payload map[string]interface{}, filter string) bool {
	clauses := strings.Split(filter, "&&")
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if strings.Contains(clause, "==") {
			sides := strings.Split(clause, "==")
			key := strings.TrimSpace(sides[0])
			expected := strings.Trim(strings.TrimSpace(sides[1]), "'\"")
			val := ResolveField(payload, key)
			if val == nil || fmt.Sprintf("%v", val) != expected {
				return false
			}
		} else if strings.Contains(clause, "!=") {
			sides := strings.Split(clause, "!=")
			key := strings.TrimSpace(sides[0])
			expected := strings.Trim(strings.TrimSpace(sides[1]), "'\"")
			val := ResolveField(payload, key)
			if expected == "null" && val == nil {
				return false
			}
		}
	}
	return true
}
