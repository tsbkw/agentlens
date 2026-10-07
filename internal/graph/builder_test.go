package graph

import (
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
)

func TestBuildDAGWithFallbacks(t *testing.T) {
	now := time.Now()

	nodes := []models.CallNode{
		{
			ID:        "node-1",
			SessionID: "sess-1",
			Type:      models.NodeTypeMCPTool,
			Name:      "mcp_github_create_issue",
			Timestamp: now,
			Status:    models.StatusFailed,
		},
		{
			ID:        "node-2",
			SessionID: "sess-1",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(time.Second),
			Status:    models.StatusSuccess,
		},
	}

	builder := NewGraphBuilder()
	g := builder.Build("sess-1", "antigravity", nodes)

	if len(g.Nodes) != 2 {
		t.Fatalf("Expected 2 nodes in graph, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Fatalf("Expected 1 edge, got %d", len(g.Edges))
	}

	edge := g.Edges[0]
	if edge.SourceID != "node-1" || edge.TargetID != "node-2" {
		t.Errorf("Unexpected edge from %s to %s", edge.SourceID, edge.TargetID)
	}
	if edge.Type != models.EdgeTypeFallbackTo {
		t.Errorf("Expected EdgeTypeFallbackTo, got %v", edge.Type)
	}

	metrics := g.ComputeMetrics()
	if metrics.TotalNodes != 2 {
		t.Errorf("Expected 2 total nodes in metrics, got %d", metrics.TotalNodes)
	}
	if metrics.FailedCalls != 1 {
		t.Errorf("Expected 1 failed call in metrics, got %d", metrics.FailedCalls)
	}
	if metrics.MCPToolCalls != 1 {
		t.Errorf("Expected 1 MCP tool call in metrics, got %d", metrics.MCPToolCalls)
	}
	if metrics.SystemCalls != 1 {
		t.Errorf("Expected 1 system tool call in metrics, got %d", metrics.SystemCalls)
	}
}

func TestBuildSubagentHierarchy(t *testing.T) {
	now := time.Now()

	nodes := []models.CallNode{
		{
			ID:        "parent-agent",
			SessionID: "sess-2",
			Type:      models.NodeTypeSubagent,
			Name:      "invoke_subagent",
			Timestamp: now,
			Status:    models.StatusSuccess,
		},
		{
			ID:        "child-tool",
			SessionID: "sess-2",
			ParentID:  "parent-agent",
			Type:      models.NodeTypeSkill,
			Name:      "skill_code_search",
			Timestamp: now.Add(time.Second),
			Status:    models.StatusSuccess,
		},
	}

	builder := NewGraphBuilder()
	g := builder.Build("sess-2", "antigravity", nodes)

	if len(g.Edges) != 1 {
		t.Fatalf("Expected 1 edge, got %d", len(g.Edges))
	}
	if g.Edges[0].Type != models.EdgeTypeSpawns {
		t.Errorf("Expected EdgeTypeSpawns, got %v", g.Edges[0].Type)
	}
}
