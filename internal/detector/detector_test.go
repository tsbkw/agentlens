package detector

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

func TestAuthAndSilentFallbackDetection(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	now := time.Now()

	// Scenario: Primary MCP tool fails with 401 auth error,
	// agent immediately executes run_command as fallback.
	nodes := []models.CallNode{
		{
			ID:           "node-mcp-1",
			SessionID:    "test-sess",
			Type:         models.NodeTypeMCPTool,
			Name:         "mcp_github_create_issue",
			MCPServer:    "github",
			Timestamp:    now,
			Status:       models.StatusFailed,
			ErrorMessage: "API call returned 401 Unauthorized: token expired",
		},
		{
			ID:        "node-shell-2",
			SessionID: "test-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(time.Second),
			Status:    models.StatusSuccess,
			Arguments: map[string]interface{}{
				"CommandLine": "gh issue create --title 'Bug'",
			},
		},
	}

	builder := graph.NewGraphBuilder()
	g := builder.Build("test-sess", "antigravity", nodes)

	detector := NewDetector(provider)
	result := detector.Analyze(g)

	if len(result.Anomalies) != 2 {
		t.Fatalf("Expected 2 anomalies (1 auth, 1 fallback), got %d", len(result.Anomalies))
	}

	// Verify Auth Anomaly
	var foundAuth, foundFallback bool
	for _, anom := range result.Anomalies {
		if anom.Type == models.AnomalyAuthExpired {
			foundAuth = true
			if anom.NodeID != "node-mcp-1" {
				t.Errorf("Expected auth anomaly on node-mcp-1, got %s", anom.NodeID)
			}
			if anom.Severity != models.SeverityCritical {
				t.Errorf("Expected Critical severity for auth expiration")
			}
		}
		if anom.Type == models.AnomalySilentFallback {
			foundFallback = true
			if anom.NodeID != "node-shell-2" {
				t.Errorf("Expected fallback anomaly on node-shell-2, got %s", anom.NodeID)
			}
			if anom.RelatedNodeID != "node-mcp-1" {
				t.Errorf("Expected related node to be node-mcp-1, got %s", anom.RelatedNodeID)
			}
		}
	}

	if !foundAuth {
		t.Errorf("Missing expected AnomalyAuthExpired")
	}
	if !foundFallback {
		t.Errorf("Missing expected AnomalySilentFallback")
	}

	// Verify edge was updated to FallbackTo
	if len(g.Edges) > 0 && g.Edges[0].Type != models.EdgeTypeFallbackTo {
		t.Errorf("Expected edge type FallbackTo, got %v", g.Edges[0].Type)
	}
}

func TestRetryLoopDetection(t *testing.T) {
	now := time.Now()

	nodes := []models.CallNode{
		{
			ID:        "call-1",
			SessionID: "loop-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now,
			Status:    models.StatusFailed,
		},
		{
			ID:        "call-2",
			SessionID: "loop-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(time.Second),
			Status:    models.StatusFailed,
		},
		{
			ID:        "call-3",
			SessionID: "loop-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(2 * time.Second),
			Status:    models.StatusFailed,
		},
	}

	builder := graph.NewGraphBuilder()
	g := builder.Build("loop-sess", "generic", nodes)

	detector := NewDetector(nil)
	result := detector.Analyze(g)

	if len(result.Anomalies) != 1 {
		t.Fatalf("Expected 1 loop anomaly, got %d", len(result.Anomalies))
	}

	if result.Anomalies[0].Type != models.AnomalyRetryLoop {
		t.Errorf("Expected AnomalyRetryLoop, got %v", result.Anomalies[0].Type)
	}
}

func TestComplexIncidentResponseAnomalyDetection(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	samplePath := filepath.Join("..", "..", "examples", "traces", "sample_incident_response.jsonl")
	col := collector.NewCollector(provider)
	data, err := col.IngestSessionFile(samplePath)
	if err != nil {
		t.Fatalf("Failed to ingest sample: %v", err)
	}

	builder := graph.NewGraphBuilder()
	g := builder.BuildWithTurns("sample-incident", "antigravity", data.Turns, data.Nodes)

	det := NewDetector(provider)
	result := det.Analyze(g)

	if len(result.Anomalies) == 0 {
		t.Fatalf("Expected anomalies in sample incident response trace, got 0")
	}

	var hasAuth, hasFallback, hasLoop bool
	for _, a := range result.Anomalies {
		switch a.Type {
		case models.AnomalyAuthExpired:
			hasAuth = true
		case models.AnomalySilentFallback:
			hasFallback = true
		case models.AnomalyRetryLoop:
			hasLoop = true
		}
	}

	if !hasAuth {
		t.Errorf("Expected AnomalyAuthExpired in sample trace")
	}
	if !hasFallback {
		t.Errorf("Expected AnomalySilentFallback in sample trace")
	}
	if !hasLoop {
		t.Errorf("Expected AnomalyRetryLoop in sample trace")
	}

	// Verify dependencies
	if len(g.Dependencies) == 0 {
		t.Errorf("Expected dependencies to be computed, got 0")
	}
}
