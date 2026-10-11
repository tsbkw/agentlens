package report

import (
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
)

func TestBuilderCountsCallsAndAnomaliesInWindow(t *testing.T) {
	day := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	calls := []models.CallNode{
		{ID: "before", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day.Add(-48 * time.Hour), Status: models.StatusFailed},
		{ID: "gh", Name: "mcp__github__get_issue", MCPServer: "github", Type: models.NodeTypeMCPTool, Timestamp: day, Status: models.StatusFailed},
		{ID: "sh", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day.Add(time.Minute), Status: models.StatusSuccess},
		{ID: "no", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day.Add(2 * time.Minute), Status: models.StatusDenied},
		{ID: "inferred-subagent-x", Name: "Subagent", Type: models.NodeTypeSubagent, Timestamp: day, ParentLink: models.ParentLinkInferred},
	}
	g := graph.NewGraphBuilder().Build("s1", "claude-code", calls)
	g.Nodes["gh"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalyAuthExpired}}
	g.Nodes["sh"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalySilentFallback, Confidence: models.ConfidenceHigh}}
	g.Nodes["no"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalySilentFallback, Confidence: models.ConfidenceLow}}

	b := NewBuilder(Window{Since: day.Add(-time.Hour), Until: day.Add(time.Hour)})
	b.Add("s1", "proj-a", calls, g)
	// A session without calls in the window is not counted
	b.Add("s2", "proj-b", calls[:1], graph.NewGraphBuilder().Build("s2", "claude-code", calls[:1]))
	r := b.Report()

	want := Counts{Calls: 3, Failed: 1, Denied: 1, AuthExpired: 1, SilentFallbacks: 1, PossibleFallbacks: 1}
	if r.Sessions != 1 || r.Total != want {
		t.Fatalf("Expected 1 session with %+v, got %d sessions with %+v", want, r.Sessions, r.Total)
	}
	if len(r.MCPServers) != 1 || r.MCPServers[0].Key != "github" || r.MCPServers[0].AuthExpired != 1 {
		t.Errorf("Unexpected MCP server rows %+v", r.MCPServers)
	}
	// Bash has 2 anomalies, the MCP tool 1: sorted by anomalies first
	if len(r.Tools) != 2 || r.Tools[0].Key != "Bash" || r.Tools[0].Calls != 2 {
		t.Errorf("Unexpected tool rows %+v", r.Tools)
	}
	if r.BySession[0].Key != "s1" || r.BySession[0].Project != "proj-a" || r.BySession[0].Provider != "claude-code" {
		t.Errorf("Unexpected session rows %+v", r.BySession)
	}
}

func TestWindowContains(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	w := Window{Since: since, Until: until}
	if !w.Contains(since) || w.Contains(until) || w.Contains(since.Add(-time.Second)) {
		t.Errorf("Window must include Since and exclude Until")
	}
	if !(Window{}).Contains(since) {
		t.Errorf("An empty window contains everything")
	}
}
