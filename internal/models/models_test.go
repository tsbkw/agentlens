package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCallNodeSerialization(t *testing.T) {
	node := CallNode{
		ID:        "node-1",
		SessionID: "sess-abc",
		Type:      NodeTypeMCPTool,
		Name:      "github_create_issue",
		MCPServer: "github-mcp",
		Timestamp: time.Now(),
		Status:    StatusSuccess,
		Arguments: map[string]interface{}{
			"title": "Bug in login",
		},
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("Failed to marshal CallNode: %v", err)
	}

	var parsed CallNode
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal CallNode: %v", err)
	}

	if parsed.Name != "github_create_issue" {
		t.Errorf("Expected name %q, got %q", "github_create_issue", parsed.Name)
	}
	if parsed.MCPServer != "github-mcp" {
		t.Errorf("Expected MCPServer %q, got %q", "github-mcp", parsed.MCPServer)
	}
}

func TestAnomalyRecordSerialization(t *testing.T) {
	anomaly := AnomalyRecord{
		ID:            "anom-1",
		Type:          AnomalySilentFallback,
		Severity:      SeverityWarning,
		NodeID:        "node-2",
		RelatedNodeID: "node-1",
		Title:         "Silent Fallback Detected",
		Description:   "MCP tool failed; model resorted to shell command",
	}

	data, err := json.Marshal(anomaly)
	if err != nil {
		t.Fatalf("Failed to marshal AnomalyRecord: %v", err)
	}

	var parsed AnomalyRecord
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal AnomalyRecord: %v", err)
	}

	if parsed.Type != AnomalySilentFallback {
		t.Errorf("Expected type %v, got %v", AnomalySilentFallback, parsed.Type)
	}
}
