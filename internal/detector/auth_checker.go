package detector

import (
	"fmt"
	"strings"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/textutil"
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

	for _, node := range g.OrderedNodes() {
		var contentToCheck []string
		if node.ErrorMessage != "" {
			contentToCheck = append(contentToCheck, node.ErrorMessage)
		}
		// A successful call's output is arbitrary content (files, documents, search hits, query
		// rows) that often mentions "401" or "unauthorized"; only failed calls are checked.
		if node.Output != nil && outputMayCarryAuthError(node) {
			contentToCheck = append(contentToCheck, fmt.Sprintf("%v", node.Output))
		}

		// Join with line breaks so a match is quoted from one part only (matchContext)
		fullText := strings.Join(contentToCheck, "\n")
		if fullText == "" {
			continue
		}

		for idx, re := range c.Provider.AuthFailureRegex {
			if loc := re.FindStringIndex(fullText); loc != nil {
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
						"Tool %s reported an authentication failure: %q. Execution may be degraded or blocked.",
						toolDesc, matchContext(fullText, loc, 120),
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

// matchContext returns the line of text around the match at loc, trimmed to about max
// characters centred on the match, so the alert quotes the error rather than the pattern.
func matchContext(text string, loc []int, max int) string {
	start := strings.LastIndex(text[:loc[0]], "\n") + 1
	end := len(text)
	if i := strings.Index(text[loc[1]:], "\n"); i >= 0 {
		end = loc[1] + i
	}
	line := text[start:end]
	rel := loc[0] - start
	// Keep the match visible in a long line by cutting the head
	if cut := rel - max/3; cut > 0 && len([]rune(line)) > max {
		line = "…" + line[cut:]
	}
	return strings.Join(strings.Fields(textutil.Truncate(line, max, "…")), " ")
}

func outputMayCarryAuthError(node *models.CallNode) bool {
	return node.Status == models.StatusFailed || node.Status == models.StatusTimeout
}
