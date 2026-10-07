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

func TestTransitiveMultiHopCallTree(t *testing.T) {
	now := time.Now()

	// 3-hop scenario:
	// Agent -> Subagent: Database Debugger -> Skill: query-optimizer -> mcp_postgres_query
	nodes := []models.CallNode{
		{
			ID:          "node-subagent",
			SessionID:   "sess-multihop",
			CallerScope: "Agent",
			Type:        models.NodeTypeSubagent,
			Name:        "invoke_subagent",
			Arguments:   map[string]interface{}{"Role": "Database Debugger"},
			Timestamp:   now,
			Status:      models.StatusSuccess,
		},
		{
			ID:          "node-skill",
			SessionID:   "sess-multihop",
			CallerScope: "Subagent: Database Debugger",
			Type:        models.NodeTypeSkill,
			Name:        "view_file",
			Arguments:   map[string]interface{}{"AbsolutePath": "/skills/query-optimizer/SKILL.md"},
			Timestamp:   now.Add(time.Second),
			Status:      models.StatusSuccess,
		},
		{
			ID:          "node-mcp",
			SessionID:   "sess-multihop",
			CallerScope: "Skill: query-optimizer",
			Type:        models.NodeTypeMCPTool,
			Name:        "mcp_postgres_query",
			MCPServer:   "postgres",
			Timestamp:   now.Add(2 * time.Second),
			Status:      models.StatusSuccess,
		},
	}

	builder := NewGraphBuilder()
	g := builder.Build("sess-multihop", "antigravity", nodes)

	if len(g.Dependencies) == 0 {
		t.Fatalf("Expected dependencies, got 0")
	}

	// Find the Subagent dependency under Agent
	var subagentDep *models.CallerDependency
	for i := range g.Dependencies {
		if g.Dependencies[i].Callee == "Subagent: Database Debugger" {
			subagentDep = &g.Dependencies[i]
			break
		}
	}

	if subagentDep == nil {
		t.Fatalf("Expected Subagent: Database Debugger in top-level dependencies")
	}

	// Verify Skill: query-optimizer is a child of Subagent: Database Debugger
	if len(subagentDep.Children) == 0 {
		t.Fatalf("Expected children in Subagent: Database Debugger, got 0")
	}

	var skillDep *models.CallerDependency
	for i := range subagentDep.Children {
		if subagentDep.Children[i].Callee == "Skill: query-optimizer" {
			skillDep = &subagentDep.Children[i]
			break
		}
	}

	if skillDep == nil {
		t.Fatalf("Expected Skill: query-optimizer in Subagent: Database Debugger's children")
	}

	// Verify mcp_postgres_query is a child of Skill: query-optimizer
	if len(skillDep.Children) == 0 {
		t.Fatalf("Expected children in Skill: query-optimizer, got 0")
	}

	if skillDep.Children[0].Callee != "mcp_postgres_query" {
		t.Errorf("Expected mcp_postgres_query as grandchild, got %s", skillDep.Children[0].Callee)
	}
	if skillDep.Children[0].MCPServer != "postgres" {
		t.Errorf("Expected MCPServer 'postgres', got %s", skillDep.Children[0].MCPServer)
	}
}
