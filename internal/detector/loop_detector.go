package detector

import (
	"fmt"
	"sort"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
)

// LoopDetector identifies consecutive tool call failures and retry loops.
type LoopDetector struct {
	Threshold int // Default 3
}

// NewLoopDetector creates a LoopDetector with default threshold of 3.
func NewLoopDetector() *LoopDetector {
	return &LoopDetector{Threshold: 3}
}

// Check scans chronological node sequences for repetitive failure thrashing.
func (ld *LoopDetector) Check(g *graph.Graph) []models.AnomalyRecord {
	var records []models.AnomalyRecord

	sortedNodes := make([]*models.CallNode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		sortedNodes = append(sortedNodes, n)
	}
	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].Timestamp.Before(sortedNodes[j].Timestamp)
	})

	if len(sortedNodes) < ld.Threshold {
		return records
	}

	// Track streaks per actor: calls sharing a ParentID (e.g. one subagent) form one sequence
	type streak struct {
		consecutiveFailures int
		lastFailedTool      string
		failingNodeIDs      []string
	}
	streaks := make(map[string]*streak)

	for _, node := range sortedNodes {
		st := streaks[node.ParentID]
		if st == nil {
			st = &streak{}
			streaks[node.ParentID] = st
		}
		consecutiveFailures, lastFailedTool, failingNodeIDs := st.consecutiveFailures, st.lastFailedTool, st.failingNodeIDs

		if node.Status == models.StatusFailed || node.Status == models.StatusTimeout {
			if node.Name == lastFailedTool || lastFailedTool == "" {
				consecutiveFailures++
				lastFailedTool = node.Name
				failingNodeIDs = append(failingNodeIDs, node.ID)

				if consecutiveFailures >= ld.Threshold {
					records = append(records, models.AnomalyRecord{
						ID:       fmt.Sprintf("loop-%s-%d", node.ID, consecutiveFailures),
						Type:     models.AnomalyRetryLoop,
						Severity: models.SeverityWarning,
						NodeID:   node.ID,
						Title:    fmt.Sprintf("Retry Loop / Thrashing: %s failed %d times", node.Name, consecutiveFailures),
						Description: fmt.Sprintf(
							"Tool %q failed %d times consecutively. The agent appears stuck in a retry loop without progress.",
							node.Name, consecutiveFailures,
						),
						Recommendation: "Inspect input arguments or environment state causing repeated failures.",
					})
				}
			} else {
				// Different tool failed, reset counter
				consecutiveFailures = 1
				lastFailedTool = node.Name
				failingNodeIDs = []string{node.ID}
			}
		} else {
			// Succeeded, reset counter
			consecutiveFailures = 0
			lastFailedTool = ""
			failingNodeIDs = nil
		}

		st.consecutiveFailures, st.lastFailedTool, st.failingNodeIDs = consecutiveFailures, lastFailedTool, failingNodeIDs
	}

	return records
}
