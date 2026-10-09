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

func TestParseClaudeCodeEvents(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "providers", "claude_code.yaml")
	provider, err := LoadProviderFromFile(path)
	if err != nil {
		t.Fatalf("Failed to load provider: %v", err)
	}
	parser := NewTraceParser(provider)

	samplePath := "/Users/dev/.claude/projects/-Users-dev-repo/0f9c2a6e-1111-2222-3333-444455556666.jsonl"
	if got := parser.ExtractSessionID(samplePath, nil); got != "0f9c2a6e-1111-2222-3333-444455556666" {
		t.Errorf("Expected session ID from file name, got %q", got)
	}

	rawJSON := `{
		"type": "assistant",
		"sessionId": "0f9c2a6e-1111-2222-3333-444455556666",
		"timestamp": "2026-10-07T14:00:00.123Z",
		"message": {
			"role": "assistant",
			"content": [
				{"type": "text", "text": "Let me look."},
				{"type": "tool_use", "id": "toolu_01", "name": "mcp__claude_ai_Slack__send_message", "input": {"channel": "#dev"}},
				{"type": "tool_use", "id": "toolu_02", "name": "Bash", "input": {"command": "git status"}}
			]
		}
	}`
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		t.Fatalf("Failed to unmarshal sample event: %v", err)
	}

	nodes, err := parser.ParseNodes("s1", payload, time.Now())
	if err != nil {
		t.Fatalf("ParseNodes failed: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("Expected 2 tool_use nodes (text block skipped), got %d", len(nodes))
	}

	if nodes[0].ID != "toolu_01" || nodes[0].Type != models.NodeTypeMCPTool {
		t.Errorf("Expected MCP node toolu_01, got %q (%v)", nodes[0].ID, nodes[0].Type)
	}
	if nodes[0].MCPServer != "claude_ai_Slack" {
		t.Errorf("Expected MCPServer 'claude_ai_Slack', got %q", nodes[0].MCPServer)
	}
	if nodes[0].Arguments["channel"] != "#dev" {
		t.Errorf("Expected arguments from input, got %v", nodes[0].Arguments)
	}
	if nodes[0].Timestamp.IsZero() || nodes[0].Timestamp.Year() != 2026 {
		t.Errorf("Expected timestamp from root event, got %v", nodes[0].Timestamp)
	}
	if nodes[1].Name != "Bash" || nodes[1].Type != models.NodeTypeSystemTool {
		t.Errorf("Expected Bash system tool, got %q (%v)", nodes[1].Name, nodes[1].Type)
	}

	// A user message carrying only tool_result blocks must not produce nodes
	userJSON := `{"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "toolu_01", "content": "ok"}]}}`
	var userPayload map[string]interface{}
	if err := json.Unmarshal([]byte(userJSON), &userPayload); err != nil {
		t.Fatalf("Failed to unmarshal user event: %v", err)
	}
	if nodes, _ := parser.ParseNodes("s1", userPayload, time.Now()); len(nodes) != 0 {
		t.Errorf("Expected no nodes from tool_result message, got %d", len(nodes))
	}
}

func TestEvaluateSimpleFilter(t *testing.T) {
	payload := map[string]interface{}{
		"type":  "tool_call",
		"event": "x",
		"meta":  map[string]interface{}{"source": "MODEL"},
	}
	cases := []struct {
		filter string
		want   bool
	}{
		{"type == 'tool_call'", true},
		{"type == 'other'", false},
		{"type == 'other' || event == 'x'", true},
		{"type == 'other' || event == 'y'", false},
		{"type == 'tool_call' && meta.source == 'MODEL'", true},
		{"type == 'tool_call' && meta.source != 'MODEL'", false},
		{"missing != null", false},
		{"meta != null", true},
		{"missing == null", true},
		{"meta.source", true},
		{"meta.missing", false},
	}
	for _, c := range cases {
		if got := evaluateSimpleFilter(payload, c.filter); got != c.want {
			t.Errorf("evaluateSimpleFilter(%q) = %v; want %v", c.filter, got, c.want)
		}
	}
}
