package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

type GoldenFile struct {
	Provider  string `json:"provider"`
	TurnCount int    `json:"turn_count"`
	NodeCount int    `json:"node_count"`
	Turns     []struct {
		Index           int    `json:"index"`
		PromptFirstLine string `json:"prompt_first_line"`
		TurnNodeName    string `json:"turn_node_name"`
	} `json:"turns"`
	Nodes []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		ScopeName   string `json:"scope_name"`
		CallerScope string `json:"caller_scope"`
		Status      string `json:"status"`
		Output      string `json:"output"`
	} `json:"nodes"`
	Anomalies []struct {
		Type   string `json:"type"`
		NodeID string `json:"node_id"`
	} `json:"anomalies"`
}

func TestGeminiSyntheticPipelineIntegration(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	fixturePath := filepath.Join("..", "fixtures", "synthetic", "gemini_edge_cases.jsonl")
	goldenPath := filepath.Join("..", "fixtures", "synthetic", "gemini_edge_cases_golden.json")

	// Read and unmarshal Golden File
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("Failed to read golden file: %v", err)
	}
	var golden GoldenFile
	if err := json.Unmarshal(goldenBytes, &golden); err != nil {
		t.Fatalf("Failed to unmarshal golden file: %v", err)
	}

	// 1. Ingest Synthetic Trace
	col := collector.NewCollector(provider)
	sessionData, err := col.IngestSessionFile(fixturePath)
	if err != nil {
		t.Fatalf("Failed to ingest synthetic session file: %v", err)
	}

	if len(sessionData.Turns) != golden.TurnCount {
		t.Fatalf("Expected %d turns, got %d", golden.TurnCount, len(sessionData.Turns))
	}
	if len(sessionData.Nodes) != golden.NodeCount {
		t.Fatalf("Expected %d nodes, got %d", golden.NodeCount, len(sessionData.Nodes))
	}

	// 2. Build Hierarchical Graph
	builder := graph.NewGraphBuilder()
	g := builder.BuildWithTurns("synthetic-gemini-sess", golden.Provider, sessionData.Turns, sessionData.Nodes)

	// Verify Turns and Multi-line Prompt First-Line Truncation
	if len(g.Turns) != len(golden.Turns) {
		t.Fatalf("Expected %d graph turns, got %d", len(golden.Turns), len(g.Turns))
	}
	for i, gt := range golden.Turns {
		turnNodeID := fmt.Sprintf("turn-%d", gt.Index)
		turnNode, exists := g.Nodes[turnNodeID]
		if !exists {
			t.Fatalf("Turn node %s not found in graph", turnNodeID)
		}
		if turnNode.Name != gt.TurnNodeName {
			t.Errorf("Turn node[%d] name mismatch: expected %q, got %q", i, gt.TurnNodeName, turnNode.Name)
		}
	}

	// 3. Analyze Anomalies
	det := detector.NewDetector(provider)
	detectionRes := det.Analyze(g)

	// 4. Assert Node Attributes against Golden Specification
	nodeMap := make(map[string]models.CallNode)
	for _, n := range sessionData.Nodes {
		nodeMap[n.ID] = n
	}

	for _, expectedNode := range golden.Nodes {
		actual, exists := nodeMap[expectedNode.ID]
		if !exists {
			t.Fatalf("Expected node %q not found in ingested trace", expectedNode.ID)
		}
		if actual.Name != expectedNode.Name {
			t.Errorf("Node %s name mismatch: expected %q, got %q", actual.ID, expectedNode.Name, actual.Name)
		}
		if string(actual.Type) != expectedNode.Type {
			t.Errorf("Node %s type mismatch: expected %q, got %q", actual.ID, expectedNode.Type, actual.Type)
		}
		if actual.ScopeName != expectedNode.ScopeName {
			t.Errorf("Node %s ScopeName mismatch: expected %q, got %q", actual.ID, expectedNode.ScopeName, actual.ScopeName)
		}
		if actual.CallerScope != expectedNode.CallerScope {
			t.Errorf("Node %s CallerScope mismatch: expected %q, got %q", actual.ID, expectedNode.CallerScope, actual.CallerScope)
		}
		if string(actual.Status) != expectedNode.Status {
			t.Errorf("Node %s status mismatch: expected %q, got %q", actual.ID, expectedNode.Status, actual.Status)
		}
		if actual.Output != expectedNode.Output {
			t.Errorf("Node %s output mismatch: expected %q, got %q", actual.ID, expectedNode.Output, actual.Output)
		}
	}

	// 5. Assert Anomalies against Golden Specification
	if len(detectionRes.Anomalies) != len(golden.Anomalies) {
		t.Fatalf("Expected %d anomalies, got %d: %+v", len(golden.Anomalies), len(detectionRes.Anomalies), detectionRes.Anomalies)
	}

	for i, expAnom := range golden.Anomalies {
		actAnom := detectionRes.Anomalies[i]
		if string(actAnom.Type) != expAnom.Type {
			t.Errorf("Anomaly[%d] type mismatch: expected %q, got %q", i, expAnom.Type, actAnom.Type)
		}
		if actAnom.NodeID != expAnom.NodeID {
			t.Errorf("Anomaly[%d] node ID mismatch: expected %q, got %q", i, expAnom.NodeID, actAnom.NodeID)
		}
		// Verify anomaly is properly attached to graph node
		nodeInGraph := g.Nodes[actAnom.NodeID]
		if nodeInGraph == nil {
			t.Errorf("Node %q with anomaly not found in graph", actAnom.NodeID)
		} else if len(nodeInGraph.Anomalies) == 0 {
			t.Errorf("Node %q expected to have anomaly attached, found none", actAnom.NodeID)
		}
	}
}
