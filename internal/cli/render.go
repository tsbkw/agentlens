package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
)

// RenderSessionList writes a formatted table of discovered sessions.
func RenderSessionList(w io.Writer, sessions []collector.SessionInfo) {
	if len(sessions) == 0 {
		fmt.Fprintln(w, "No recorded agent sessions found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "%sSESSION ID\tPROVIDER\tMODIFIED\tSIZE\tPATH%s\n", colorBold, colorReset)

	for _, s := range sessions {
		// Display shorter session ID if long
		displayID := s.SessionID
		if len(displayID) > 24 {
			displayID = displayID[:24] + "..."
		}

		timeStr := s.ModTime.Format("2006-01-02 15:04:05")
		sizeStr := fmt.Sprintf("%.1f KB", float64(s.SizeBytes)/1024.0)

		fmt.Fprintf(tw, "%s%s%s\t%s\t%s\t%s\t%s%s%s\n",
			colorCyan, displayID, colorReset,
			s.ProviderID,
			timeStr,
			sizeStr,
			colorDim, s.FilePath, colorReset,
		)
	}
	tw.Flush()
	fmt.Fprintf(w, "\nFound %d session(s). Use `agentlens graph <session-id>` to inspect call flow.\n", len(sessions))
}

// RenderCallGraph prints an ASCII/colored tree representing the call graph.
func RenderCallGraph(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "\n%s🔍 Call Graph: %s (%s)%s\n", colorBold, g.SessionID, g.Provider, colorReset)
	fmt.Fprintln(w, strings.Repeat("─", 60))

	if len(g.Roots) == 0 {
		fmt.Fprintln(w, "No tool calls recorded in this session.")
		return
	}

	visited := make(map[string]bool)
	for i, rootID := range g.Roots {
		isLast := i == len(g.Roots)-1
		renderNodeRecursive(w, g, rootID, "", isLast, visited)
	}

	// Print summary metrics
	metrics := g.ComputeMetrics()
	fmt.Fprintln(w, strings.Repeat("─", 60))
	fmt.Fprintf(w, "%sSummary:%s Nodes: %d | Edges: %d | MCP Calls: %d | Skill Calls: %d | System Calls: %d | Failed: %d\n",
		colorBold, colorReset,
		metrics.TotalNodes, metrics.TotalEdges,
		metrics.MCPToolCalls, metrics.SkillCalls, metrics.SystemCalls, metrics.FailedCalls,
	)
}

func renderNodeRecursive(w io.Writer, g *graph.Graph, nodeID string, prefix string, isLast bool, visited map[string]bool) {
	node := g.Nodes[nodeID]
	if node == nil {
		return
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}

	// Status badge
	statusBadge := fmt.Sprintf("%s✓%s", colorGreen, colorReset)
	if node.Status == models.StatusFailed {
		statusBadge = fmt.Sprintf("%s✗%s", colorRed, colorReset)
	} else if node.Status == models.StatusTimeout {
		statusBadge = fmt.Sprintf("%s⏱%s", colorYellow, colorReset)
	}

	// Type badge
	typeColor := colorBlue
	switch node.Type {
	case models.NodeTypeMCPTool:
		typeColor = colorCyan
	case models.NodeTypeSkill:
		typeColor = colorGreen
	case models.NodeTypeSubagent:
		typeColor = colorMagenta
	}
	typeBadge := fmt.Sprintf("%s[%s]%s", typeColor, node.Type, colorReset)

	// Anomaly alerts
	anomalyBadge := ""
	for _, anom := range node.Anomalies {
		if anom.Type == models.AnomalyAuthExpired {
			anomalyBadge += fmt.Sprintf(" %s🚨 AUTH EXPIRED%s", colorRed, colorReset)
		} else if anom.Type == models.AnomalySilentFallback {
			anomalyBadge += fmt.Sprintf(" %s⚠️ SILENT FALLBACK%s", colorYellow, colorReset)
		} else if anom.Type == models.AnomalyRetryLoop {
			anomalyBadge += fmt.Sprintf(" %s🔁 RETRY LOOP%s", colorYellow, colorReset)
		}
	}

	mcpInfo := ""
	if node.MCPServer != "" {
		mcpInfo = fmt.Sprintf(" %s(server: %s)%s", colorDim, node.MCPServer, colorReset)
	}

	fmt.Fprintf(w, "%s%s%s %s %s%s%s%s%s\n",
		prefix, connector, statusBadge, typeBadge, colorBold, node.Name, colorReset, mcpInfo, anomalyBadge,
	)

	visited[nodeID] = true
	children := g.Children[nodeID]

	newPrefix := prefix + "│   "
	if isLast {
		newPrefix = prefix + "    "
	}

	for i, childID := range children {
		childLast := i == len(children)-1
		renderNodeRecursive(w, g, childID, newPrefix, childLast, visited)
	}
}

// RenderNodeInspect prints full details of a specific node.
func RenderNodeInspect(w io.Writer, node *models.CallNode) {
	fmt.Fprintf(w, "\n%s🔍 Call Node Detail: %s%s\n", colorBold, node.ID, colorReset)
	fmt.Fprintln(w, strings.Repeat("─", 60))

	fmt.Fprintf(w, "  %sName:%s        %s\n", colorBold, colorReset, node.Name)
	fmt.Fprintf(w, "  %sType:%s        %s\n", colorBold, colorReset, node.Type)
	if node.MCPServer != "" {
		fmt.Fprintf(w, "  %sMCP Server:%s  %s\n", colorBold, colorReset, node.MCPServer)
	}
	fmt.Fprintf(w, "  %sSession:%s     %s\n", colorBold, colorReset, node.SessionID)
	if node.ParentID != "" {
		fmt.Fprintf(w, "  %sParent ID:%s   %s\n", colorBold, colorReset, node.ParentID)
	}
	fmt.Fprintf(w, "  %sTimestamp:%s   %s\n", colorBold, colorReset, node.Timestamp.Format("2006-01-02 15:04:05.000"))
	fmt.Fprintf(w, "  %sStatus:%s      %s\n", colorBold, colorReset, node.Status)

	if node.ErrorMessage != "" {
		fmt.Fprintf(w, "  %sError:%s       %s%s%s\n", colorBold, colorReset, colorRed, node.ErrorMessage, colorReset)
	}

	// Print arguments as indented JSON
	if len(node.Arguments) > 0 {
		fmt.Fprintf(w, "\n%sArguments:%s\n", colorBold, colorReset)
		if argsJSON, err := json.MarshalIndent(node.Arguments, "  ", "  "); err == nil {
			fmt.Fprintf(w, "  %s\n", string(argsJSON))
		}
	}

	// Print output if present
	if node.Output != nil {
		fmt.Fprintf(w, "\n%sOutput:%s\n", colorBold, colorReset)
		outStr := fmt.Sprintf("%v", node.Output)
		if len(outStr) > 500 {
			outStr = outStr[:500] + "... (truncated)"
		}
		fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(outStr, "\n", "\n  "))
	}

	// Print anomalies if any
	if len(node.Anomalies) > 0 {
		fmt.Fprintf(w, "\n%sDetected Anomalies & Alerts:%s\n", colorBold, colorReset)
		for _, anom := range node.Anomalies {
			sevColor := colorYellow
			if anom.Severity == models.SeverityCritical {
				sevColor = colorRed
			}
			fmt.Fprintf(w, "  • %s[%s] %s%s\n", sevColor, anom.Severity, anom.Title, colorReset)
			fmt.Fprintf(w, "    %s\n", anom.Description)
			if anom.Recommendation != "" {
				fmt.Fprintf(w, "    %sRecommendation:%s %s\n", colorCyan, colorReset, anom.Recommendation)
			}
		}
	}
	fmt.Fprintln(w, strings.Repeat("─", 60))
}
