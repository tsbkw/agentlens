package detector

import (
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// Detector orchestrates anomaly detection rules across a Call Graph.
type Detector struct {
	Provider        *providers.LoadedProvider
	AuthChecker     *AuthChecker
	FallbackDetector *FallbackDetector
	LoopDetector    *LoopDetector
}

// NewDetector creates a new Detector configured with the active provider.
func NewDetector(provider *providers.LoadedProvider) *Detector {
	return &Detector{
		Provider:        provider,
		AuthChecker:     NewAuthChecker(provider),
		FallbackDetector: NewFallbackDetector(provider),
		LoopDetector:    NewLoopDetector(),
	}
}

// DetectionResult encapsulates all detected anomalies for a session graph.
type DetectionResult struct {
	SessionID string                 `json:"session_id"`
	Anomalies []models.AnomalyRecord `json:"anomalies"`
}

// Analyze runs all anomaly detection passes across the Graph.
func (d *Detector) Analyze(g *graph.Graph) *DetectionResult {
	var allAnomalies []models.AnomalyRecord

	// 1. Auth failure & credential expiration detection
	authAnomalies := d.AuthChecker.Check(g)
	allAnomalies = append(allAnomalies, authAnomalies...)

	// 2. Silent fallback detection (e.g. MCP failed -> fell back to shell)
	fallbackAnomalies := d.FallbackDetector.Check(g)
	allAnomalies = append(allAnomalies, fallbackAnomalies...)

	// 3. Retry loops & thrashing detection
	loopAnomalies := d.LoopDetector.Check(g)
	allAnomalies = append(allAnomalies, loopAnomalies...)

	// Attach anomalies to their respective nodes in the graph
	for _, anom := range allAnomalies {
		if node, exists := g.Nodes[anom.NodeID]; exists {
			node.Anomalies = append(node.Anomalies, anom)
		}
	}

	return &DetectionResult{
		SessionID: g.SessionID,
		Anomalies: allAnomalies,
	}
}
