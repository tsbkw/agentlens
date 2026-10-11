package detector

import (
	"fmt"

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

	sortedNodes := g.OrderedNodes()

	if len(sortedNodes) < ld.Threshold {
		return records
	}

	// Track streaks per actor: calls sharing a ParentID (e.g. one subagent) form one sequence
	type streak struct {
		consecutiveFailures int
		lastFailedTool      string
		failingNodeIDs      []string
		record              int // index in records of this streak's anomaly, -1 before the threshold
	}
	streaks := make(map[string]*streak)

	for _, node := range sortedNodes {
		// Bookkeeping and denied calls neither extend nor break a streak of attempts, and
		// the builder's scope nodes are not calls at all
		if node.Auxiliary || node.Status == models.StatusDenied || (node.Synthetic && node.Type != models.NodeTypeUserTurn) {
			continue
		}
		st := streaks[node.ParentID]
		if st == nil {
			st = &streak{record: -1}
			streaks[node.ParentID] = st
		}
		consecutiveFailures, lastFailedTool, failingNodeIDs := st.consecutiveFailures, st.lastFailedTool, st.failingNodeIDs

		if node.Status == models.StatusFailed || node.Status == models.StatusTimeout {
			if node.Name == lastFailedTool || lastFailedTool == "" {
				consecutiveFailures++
				lastFailedTool = node.Name
				failingNodeIDs = append(failingNodeIDs, node.ID)

				// One anomaly per streak: raised at the threshold, its count updated as it grows
				if consecutiveFailures == ld.Threshold {
					st.record = len(records)
					records = append(records, models.AnomalyRecord{
						ID:             fmt.Sprintf("loop-%s", node.ID),
						Type:           models.AnomalyRetryLoop,
						Severity:       models.SeverityWarning,
						NodeID:         node.ID,
						Recommendation: "Inspect input arguments or environment state causing repeated failures.",
					})
				}
				if st.record >= 0 {
					r := &records[st.record]
					r.Title = fmt.Sprintf("Retry Loop / Thrashing: %s failed %d times", node.Name, consecutiveFailures)
					r.Description = fmt.Sprintf(
						"Tool %q failed %d times consecutively. The agent appears stuck in a retry loop without progress.",
						node.Name, consecutiveFailures,
					)
				}
			} else {
				// Different tool failed, reset counter
				st.record = -1
				consecutiveFailures = 1
				lastFailedTool = node.Name
				failingNodeIDs = []string{node.ID}
			}
		} else {
			// Succeeded, reset counter
			st.record = -1
			consecutiveFailures = 0
			lastFailedTool = ""
			failingNodeIDs = nil
		}

		st.consecutiveFailures, st.lastFailedTool, st.failingNodeIDs = consecutiveFailures, lastFailedTool, failingNodeIDs
	}

	return records
}
