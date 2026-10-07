package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
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
