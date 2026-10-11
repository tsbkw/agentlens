package detector

import (
	"fmt"
	"strings"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// FallbackDetector identifies silent tool substitution patterns.
type FallbackDetector struct {
	Provider *providers.LoadedProvider
	// Substitutes maps public services to the CLIs and hosts that can replace their MCP
	// servers; it raises the confidence of a fallback that uses them.
	Substitutes *SubstituteCatalog
}

// NewFallbackDetector creates a FallbackDetector with the built-in substitutes catalog.
func NewFallbackDetector(provider *providers.LoadedProvider) *FallbackDetector {
	return &FallbackDetector{Provider: provider, Substitutes: DefaultSubstitutes}
}

// Check evaluates graph edges and sequences for silent fallback transitions.
func (d *FallbackDetector) Check(g *graph.Graph) []models.AnomalyRecord {
	var records []models.AnomalyRecord

	for i := range g.Edges {
		edge := &g.Edges[i]
		sourceNode := g.Nodes[edge.SourceID]
		targetNode := g.Nodes[edge.TargetID]

		if sourceNode == nil || targetNode == nil {
			continue
		}

		// Source must have failed, timed out, or had auth issues
		sourceHadFailure := sourceNode.Status == models.StatusFailed ||
			sourceNode.Status == models.StatusTimeout ||
			nodeHasAuthAnomaly(sourceNode)

		if !sourceHadFailure {
			continue
		}

		// Check if transition matches a known fallback rule
		isKnownFallback, ruleDesc := d.matchesKnownFallback(sourceNode.Name, targetNode.Name)

		// Generic fallback heuristic:
		// If an MCP/Skill tool failed and was followed by a System tool (e.g. shell command)
		isGenericFallback := (sourceNode.Type == models.NodeTypeMCPTool || sourceNode.Type == models.NodeTypeSkill) &&
			(targetNode.Type == models.NodeTypeSystemTool)

		if isKnownFallback || isGenericFallback {
			// Rewire edge to fallback_to
			edge.Type = models.EdgeTypeFallbackTo

			desc := ruleDesc
			if desc == "" {
				desc = fmt.Sprintf(
					"Intended tool %q failed; the agent silently attempted fallback via substitute tool %q without user notification.",
					sourceNode.Name, targetNode.Name,
				)
			}
			confidence, evidence := fallbackEvidence(d.Substitutes, sourceNode, targetNode)
			title := fmt.Sprintf("Silent Fallback Detected: %s → %s", sourceNode.Name, targetNode.Name)
			if confidence == models.ConfidenceLow {
				title = fmt.Sprintf("Possible Silent Fallback (low confidence): %s → %s", sourceNode.Name, targetNode.Name)
				if ruleDesc == "" {
					desc = fmt.Sprintf("Tool %q failed and %q ran next in the same sequence; it may be a substitute.", sourceNode.Name, targetNode.Name)
				}
			}
			desc = fmt.Sprintf("%s Evidence: %s.", desc, evidence)

			records = append(records, models.AnomalyRecord{
				ID:            fmt.Sprintf("fallback-%s-%s", sourceNode.ID, targetNode.ID),
				Type:          models.AnomalySilentFallback,
				Severity:      models.SeverityWarning,
				NodeID:        targetNode.ID,
				RelatedNodeID: sourceNode.ID,
				Title:         title,
				Description:   desc,
				Confidence:    confidence,
				Evidence:      evidence,
				Recommendation: fmt.Sprintf(
					"Resolve the failure on primary tool %q. Falling back to %q often produces degraded or incomplete results.",
					sourceNode.Name, targetNode.Name,
				),
			})
		}
	}

	return records
}

func (d *FallbackDetector) matchesKnownFallback(sourceTool, targetTool string) (bool, string) {
	if d.Provider == nil {
		return false, ""
	}

	for _, rule := range d.Provider.Definition.AnomalyRules.KnownFallbacks {
		origMatch := matchToolPattern(rule.OriginalTool, sourceTool)
		fallbackMatch := matchToolPattern(rule.FallbackTool, targetTool)

		if origMatch && fallbackMatch {
			return true, rule.Description
		}
	}

	return false, ""
}

func matchToolPattern(pattern, toolName string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(toolName, prefix)
	}
	return pattern == toolName
}

func nodeHasAuthAnomaly(node *models.CallNode) bool {
	for _, a := range node.Anomalies {
		if a.Type == models.AnomalyAuthExpired {
			return true
		}
	}
	return false
}
