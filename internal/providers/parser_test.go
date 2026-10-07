package providers

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
)

func TestParseAntigravityEvents(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := LoadProviderFromFile(path)
	if err != nil {
		t.Fatalf("Failed to load provider: %v", err)
	}

	parser := NewTraceParser(provider)

	// Test Session ID extraction
	samplePath := "/home/user/.gemini/antigravity-cli/brain/sess-12345/system_generated/logs/transcript.jsonl"
	sessionID := parser.ExtractSessionID(samplePath, nil)
	if sessionID != "sess-12345" {
		t.Errorf("Expected session ID 'sess-12345', got %q", sessionID)
	}

	// Test Tool Call Event Detection & Extraction
	rawJSON := `{
		"step_index": 4,
		"type": "PLANNER_RESPONSE",
		"status": "DONE",
		"created_at": "2026-10-07T14:00:00Z",
		"tool_calls": [
			{
				"id": "tc-1",
				"name": "mcp_github_create_issue",
				"arguments": {"title": "Test Issue"},
				"output": {"number": 42}
			},
			{
				"id": "tc-2",
				"name": "run_command",
				"arguments": {"CommandLine": "git status"},
				"output": "Clean"
			}
		]
	}`

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		t.Fatalf("Failed to unmarshal sample event: %v", err)
	}

	if !parser.IsToolCallEvent(payload) {
		t.Fatalf("Expected IsToolCallEvent to be true")
	}

	nodes, err := parser.ParseNodes("sess-12345", payload, time.Now())
	if err != nil {
		t.Fatalf("ParseNodes failed: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("Expected 2 nodes, got %d", len(nodes))
	}

	// Verify first node (MCP Tool)
	node1 := nodes[0]
	if node1.ID != "tc-1" {
		t.Errorf("Expected ID 'tc-1', got %q", node1.ID)
	}
	if node1.Type != models.NodeTypeMCPTool {
		t.Errorf("Expected Type NodeTypeMCPTool, got %v", node1.Type)
	}
	if node1.MCPServer != "github" {
		t.Errorf("Expected MCPServer 'github', got %q", node1.MCPServer)
	}
	if node1.Status != models.StatusSuccess {
		t.Errorf("Expected Status Success, got %v", node1.Status)
	}

	// Verify second node (System Tool)
	node2 := nodes[1]
	if node2.ID != "tc-2" {
		t.Errorf("Expected ID 'tc-2', got %q", node2.ID)
	}
	if node2.Type != models.NodeTypeSystemTool {
		t.Errorf("Expected Type NodeTypeSystemTool, got %v", node2.Type)
	}
}

func TestClassifyToolType(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		expected models.CallNodeType
	}{
		{"invoke_subagent", "", models.NodeTypeSubagent},
		{"mcp_slack_post", "", models.NodeTypeMCPTool},
		{"custom_tool", "mcp", models.NodeTypeMCPTool},
		{"skill_run_task", "", models.NodeTypeSkill},
		{"run_command", "", models.NodeTypeSystemTool},
	}

	for _, c := range cases {
		got := classifyToolType(c.name, c.explicit)
		if got != c.expected {
			t.Errorf("classifyToolType(%q, %q) = %v; want %v", c.name, c.explicit, got, c.expected)
		}
	}
}
