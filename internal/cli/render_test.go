package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

func TestRenderSessionList(t *testing.T) {
	sessions := []collector.SessionInfo{
		{
			SessionID:  "sess-xyz-12345",
			ProviderID: "antigravity",
			ModTime:    time.Now(),
			SizeBytes:  1024,
			FilePath:   "/path/to/transcript.jsonl",
		},
	}

	var buf bytes.Buffer
	RenderSessionList(&buf, sessions)
	output := buf.String()

	if !strings.Contains(output, "sess-xyz-12345") {
		t.Errorf("Expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "antigravity") {
		t.Errorf("Expected provider in output, got: %s", output)
	}
}

func TestRenderCallGraphAndInspect(t *testing.T) {
	now := time.Now()
	nodes := []models.CallNode{
		{
			ID:        "node-1",
			SessionID: "sess-abc",
			Type:      models.NodeTypeMCPTool,
			Name:      "mcp_github_issue",
			MCPServer: "github",
			Timestamp: now,
			Status:    models.StatusSuccess,
			Arguments: map[string]interface{}{"title": "Bug"},
		},
	}

	builder := graph.NewGraphBuilder()
	g := builder.Build("sess-abc", "antigravity", nodes)

	var buf bytes.Buffer
	RenderCallGraph(&buf, g)
	graphOut := buf.String()

	if !strings.Contains(graphOut, "mcp_github_issue") {
		t.Errorf("Expected tool name in graph output, got: %s", graphOut)
	}

	buf.Reset()
	RenderNodeInspect(&buf, &nodes[0])
	inspectOut := buf.String()

	if !strings.Contains(inspectOut, "mcp_github_issue") {
		t.Errorf("Expected tool name in inspect output, got: %s", inspectOut)
	}
	if !strings.Contains(inspectOut, "github") {
		t.Errorf("Expected server name in inspect output, got: %s", inspectOut)
	}
}

func TestRenderCallGraphIsDeterministic(t *testing.T) {
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load provider: %v", err)
	}

	// Parallel tool calls from one model response share a timestamp; several fail with auth errors.
	now := time.Now()
	var nodes []models.CallNode
	for _, name := range []string{"mcp__slack__post", "mcp__github__issue", "mcp__jira__get", "mcp__notion__fetch"} {
		nodes = append(nodes, models.CallNode{
			ID: "id-" + name, SessionID: "s", TurnIndex: 1, Type: models.NodeTypeMCPTool, Name: name,
			Timestamp: now, Status: models.StatusFailed, ErrorMessage: "401 Unauthorized",
		})
	}
	turns := []models.ExecutionTurn{{Index: 1, Prompt: "go", Timestamp: now}}

	render := func() string {
		g := graph.NewGraphBuilder().BuildWithTurns("s", "claude_code", turns, nodes)
		detector.NewDetector(provider).Analyze(g)
		var buf bytes.Buffer
		RenderCallGraph(&buf, g)
		return buf.String()
	}

	first := render()
	for i := 0; i < 20; i++ {
		if out := render(); out != first {
			t.Fatalf("Output changed between runs:\n--- first\n%s\n--- run %d\n%s", first, i, out)
		}
	}

	// Anomalies follow the input order of calls sharing a timestamp
	last := -1
	for _, n := range nodes {
		idx := strings.Index(first, "Authentication Expired or Failed: "+n.Name)
		if idx < 0 || idx < last {
			t.Fatalf("Expected auth anomaly for %s after the previous one, got output:\n%s", n.Name, first)
		}
		last = idx
	}
}
