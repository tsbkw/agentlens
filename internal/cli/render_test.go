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
	"github.com/tsbkw/agentlens/internal/report"
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

func TestRenderMarksInferredSubagents(t *testing.T) {
	now := time.Now()
	placeholder := models.CallNode{
		ID: "inferred-subagent-x", SessionID: "s", TurnIndex: 1, Type: models.NodeTypeSubagent, Name: "Subagent",
		CallerScope: "Agent", ScopeName: "Subagent: background-check", Timestamp: now, Status: models.StatusSuccess,
		ParentLink: models.ParentLinkInferred,
	}
	nodes := []models.CallNode{
		{ID: "a", SessionID: "s", TurnIndex: 1, Type: models.NodeTypeSystemTool, Name: "Bash", CallerScope: "Agent", Timestamp: now, Status: models.StatusSuccess},
		placeholder,
		{ID: "u", SessionID: "s", TurnIndex: 1, ParentID: placeholder.ID, ParentLink: models.ParentLinkInferred, Type: models.NodeTypeSystemTool,
			Name: "Grep", CallerScope: "Subagent: background-check", Timestamp: now.Add(time.Second), Status: models.StatusSuccess},
	}
	turns := []models.ExecutionTurn{{Index: 1, Prompt: "go", Timestamp: now}}
	g := graph.NewGraphBuilder().BuildWithTurns("s", "claude-code", turns, nodes)

	var buf bytes.Buffer
	RenderExecutionTrace(&buf, g)
	if !strings.Contains(buf.String(), "Subagent: background-check") || !strings.Contains(buf.String(), "inferred: spawning call not recorded") {
		t.Errorf("Expected the inferred subagent to be marked in the trace:\n%s", buf.String())
	}

	buf.Reset()
	RenderCallGraph(&buf, g)
	if !strings.Contains(buf.String(), "inferred: spawning call not recorded") {
		t.Errorf("Expected the inferred dependency to be marked in the graph:\n%s", buf.String())
	}

	buf.Reset()
	RenderNodeInspect(&buf, &nodes[2])
	if !strings.Contains(buf.String(), "Parent link:") || !strings.Contains(buf.String(), "inferred") {
		t.Errorf("Expected the parent link in inspect output:\n%s", buf.String())
	}
}

func TestRenderReport(t *testing.T) {
	r := &report.Report{
		Window:   report.Window{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		Sessions: 1,
		Total:    report.Counts{Calls: 3, Failed: 1, AuthExpired: 1},
		MCPServers: []report.Row{
			{Key: "github", Counts: report.Counts{Calls: 1, Failed: 1, AuthExpired: 1}},
		},
		Tools:     []report.Row{{Key: "a"}, {Key: "b"}, {Key: "c"}},
		BySession: []report.Row{{Key: "sess-1", Project: "repo", Counts: report.Counts{Calls: 3}}},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, 2)
	out := buf.String()
	for _, want := range []string{"2026-10-01 00:00 → now", "Sessions: 1 | Calls: 3 | Failed: 1", "auth expired 1", "github", "… 1 more", "sess-1", "repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("Expected %q in report output:\n%s", want, out)
		}
	}
}
