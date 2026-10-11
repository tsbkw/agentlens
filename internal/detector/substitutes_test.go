package detector

import (
	"strings"
	"testing"

	"github.com/tsbkw/agentlens/internal/models"
)

func TestFallbackEvidence(t *testing.T) {
	call := func(name, server string, args map[string]interface{}) *models.CallNode {
		return &models.CallNode{Name: name, MCPServer: server, Arguments: args}
	}
	issue := call("mcp__github__get_issue", "github", map[string]interface{}{"owner": "acme", "repo": "widgets", "issue_number": 42.0})

	cases := []struct {
		name     string
		source   *models.CallNode
		target   *models.CallNode
		want     models.Confidence
		evidence string
	}{
		{"catalog CLI", issue, call("Bash", "", map[string]interface{}{"command": "cd /tmp && gh issue view 42"}), models.ConfidenceHigh, "runs gh, the GitHub CLI"},
		{"catalog host", call("mcp__claude_ai_Notion__fetch", "claude_ai_Notion", map[string]interface{}{"id": "abc"}),
			call("WebFetch", "", map[string]interface{}{"url": "https://api.notion.com/v1/pages/abc"}), models.ConfidenceHigh, "calls api.notion.com (Notion)"},
		{"CLI name inside a word is not a command", issue, call("Bash", "", map[string]interface{}{"command": "ls ~/.ghcache"}), models.ConfidenceLow, ""},
		{"shared identifiers without catalog entry", call("mcp__warehouse__describe_table", "warehouse", map[string]interface{}{"dataset": "sales_eu", "table": "orders_2026"}),
			call("Bash", "", map[string]interface{}{"command": "dbcli describe sales_eu.orders_2026"}), models.ConfidenceHigh, "both calls use orders_2026, sales_eu"},
		{"one shared identifier is not enough", issue, call("Read", "", map[string]interface{}{"file_path": "/src/widgets/README.md"}), models.ConfidenceLow, "only the order"},
		{"dates do not count", call("mcp__calendar__list", "calendar", map[string]interface{}{"from": "2026-08-26", "to": "2026-08-27"}),
			call("Bash", "", map[string]interface{}{"command": "echo 2026-08-26 2026-08-27"}), models.ConfidenceLow, "only the order"},
		{"unrelated next step", issue, call("Bash", "", map[string]interface{}{"command": "go test ./..."}), models.ConfidenceLow, "only the order"},
	}
	for _, c := range cases {
		got, evidence := fallbackEvidence(DefaultSubstitutes, c.source, c.target)
		if got != c.want || !strings.Contains(evidence, c.evidence) {
			t.Errorf("%s: got %s (%q), want %s containing %q", c.name, got, evidence, c.want, c.evidence)
		}
	}
}

func TestSubstituteCatalogListsOnlyPublicServices(t *testing.T) {
	if len(DefaultSubstitutes.Services) == 0 {
		t.Fatal("Expected the built-in catalog to load")
	}
	for _, s := range DefaultSubstitutes.Services {
		if s.Name == "" || len(s.MCPServers) == 0 || len(s.Commands)+len(s.Hosts) == 0 {
			t.Errorf("Incomplete catalog entry %+v", s)
		}
	}
	if DefaultSubstitutes.ServiceFor("plugin_github_tools").Name != "GitHub" || DefaultSubstitutes.ServiceFor("in-house") != nil {
		t.Errorf("Unexpected service lookup")
	}
}
