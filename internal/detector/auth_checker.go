package detector

import (
	"fmt"
	"strings"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// AuthChecker scans call nodes for authentication failures and expired credentials.
type AuthChecker struct {
	Provider *providers.LoadedProvider
}

// NewAuthChecker creates an AuthChecker.
func NewAuthChecker(provider *providers.LoadedProvider) *AuthChecker {
	return &AuthChecker{Provider: provider}
}

// Check evaluates all nodes in the Graph for authentication expiration patterns.
func (c *AuthChecker) Check(g *graph.Graph) []models.AnomalyRecord {
	var records []models.AnomalyRecord

	for _, node := range g.Nodes {
		var contentToCheck []string
		if node.ErrorMessage != "" {
			contentToCheck = append(contentToCheck, node.ErrorMessage)
		}
		// Successful system tools routinely print arbitrary text (file contents, search hits),
		// so their output is only checked when the call failed. MCP tools may report auth
		// errors inside a successful response, so theirs is always checked.
		if node.Output != nil && outputMayCarryAuthError(node) {
			contentToCheck = append(contentToCheck, fmt.Sprintf("%v", node.Output))
		}

		fullText := strings.Join(contentToCheck, " ")
		if fullText == "" {
			continue
		}

		for idx, re := range c.Provider.AuthFailureRegex {
			if re.MatchString(fullText) {
				toolDesc := node.Name
				if node.MCPServer != "" {
					toolDesc = fmt.Sprintf("%s (MCP server: %s)", node.Name, node.MCPServer)
				}

				records = append(records, models.AnomalyRecord{
					ID:       fmt.Sprintf("auth-exp-%s-%d", node.ID, idx),
					Type:     models.AnomalyAuthExpired,
					Severity: models.SeverityCritical,
					NodeID:   node.ID,
					Title:    fmt.Sprintf("Authentication Expired or Failed: %s", node.Name),
					Description: fmt.Sprintf(
						"Tool %s encountered authentication failure matching pattern %q. Execution may be degraded or blocked.",
						toolDesc, re.String(),
					),
					Recommendation: "Verify credentials, renew OAuth tokens, or check environment variables for this tool/MCP server.",
				})
				// Found a matching auth error pattern for this node, avoid duplicate auth warnings on same node
				break
			}
		}
	}

	return records
}

func outputMayCarryAuthError(node *models.CallNode) bool {
	if node.Status == models.StatusFailed || node.Status == models.StatusTimeout {
		return true
	}
	return node.Type == models.NodeTypeMCPTool
}
