package collector

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

func TestIngestReaderAntigravity(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	loaded, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	col := NewCollector(loaded)

	sampleJSONL := `
{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-07T14:00:00Z","content":"Please check git status"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-07T14:00:01Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"git status"}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-10-07T14:00:02Z","content":"On branch main\nnothing to commit"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-07T14:00:03Z","tool_calls":[{"name":"mcp_github_create_issue","args":{"title":"Deploy Bug"}}]}
{"step_index":4,"source":"MODEL","type":"GENERIC","status":"ERROR","created_at":"2026-10-07T14:00:04Z","content":"401 Unauthorized: token expired"}
`

	reader := strings.NewReader(strings.TrimSpace(sampleJSONL))
	nodes, err := col.IngestReader(reader, "/home/user/.gemini/antigravity-cli/brain/sess-test-42/.system_generated/logs/transcript.jsonl")
	if err != nil {
		t.Fatalf("IngestReader failed: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("Expected 2 CallNodes, got %d", len(nodes))
	}

	// First node: run_command
	if nodes[0].Name != "run_command" {
		t.Errorf("Expected node[0] name 'run_command', got %q", nodes[0].Name)
	}
	if nodes[0].Output != "On branch main\nnothing to commit" {
		t.Errorf("Expected node[0] output to be correlated, got %v", nodes[0].Output)
	}

	// Second node: mcp_github_create_issue (failed)
	if nodes[1].Name != "mcp_github_create_issue" {
		t.Errorf("Expected node[1] name 'mcp_github_create_issue', got %q", nodes[1].Name)
	}
	if nodes[1].Status != models.StatusFailed {
		t.Errorf("Expected node[1] status to be Failed, got %v", nodes[1].Status)
	}
	if nodes[1].Output != "401 Unauthorized: token expired" {
		t.Errorf("Expected node[1] output '401 Unauthorized...', got %v", nodes[1].Output)
	}
}
