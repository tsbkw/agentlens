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

// RenderCallGraph prints the caller-to-callee dependency call graph and session overview.
func RenderCallGraph(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "\n%s🔍 Caller ➔ Callee Call Graph: %s (%s)%s\n", colorBold, g.SessionID, g.Provider, colorReset)
	fmt.Fprintf(w, "%sTraces where each Skill, Subagent, and MCP tool was invoked from.%s\n", colorDim, colorReset)
	fmt.Fprintln(w, strings.Repeat("─", 65))

	if len(g.Dependencies) == 0 && len(g.Nodes) == 0 {
		fmt.Fprintln(w, "No tool calls recorded in this session.")
		return
	}

	RenderCallerDependencies(w, g)

	// Print summary metrics
	metrics := g.ComputeMetrics()
	fmt.Fprintln(w, strings.Repeat("─", 65))
	fmt.Fprintf(w, "%sSummary:%s Nodes: %d | Edges: %d | MCP Calls: %d | Skill Calls: %d | System Calls: %d | Failed: %d\n",
		colorBold, colorReset,
		metrics.TotalNodes, metrics.TotalEdges,
		metrics.MCPToolCalls, metrics.SkillCalls, metrics.SystemCalls, metrics.FailedCalls,
	)

	// Anomaly highlights
	var allAnomalies []models.AnomalyRecord
	for _, n := range g.Nodes {
		allAnomalies = append(allAnomalies, n.Anomalies...)
	}

	if len(allAnomalies) > 0 {
		fmt.Fprintf(w, "\n%s⚠️  Detected %d Anomaly Alert(s):%s\n", colorYellow, len(allAnomalies), colorReset)
		for _, a := range allAnomalies {
			badge := fmt.Sprintf("%s[%s]%s", colorYellow, a.Severity, colorReset)
			if a.Severity == models.SeverityCritical {
				badge = fmt.Sprintf("%s[%s]%s", colorRed, a.Severity, colorReset)
			}
			fmt.Fprintf(w, "  • %s %s%s%s: %s\n", badge, colorBold, a.Title, colorReset, a.Description)
			if a.Recommendation != "" {
				fmt.Fprintf(w, "    %s💡 Fix:%s %s\n", colorCyan, colorReset, a.Recommendation)
			}
		}
	}

	fmt.Fprintf(w, "\n%s💡 Tip:%s Use `agentlens trace %s` to view the turn-by-turn chronological execution trace.\n\n",
		colorCyan, colorReset, g.SessionID,
	)
}

// RenderCallerDependencies groups and renders the caller -> callee relationships as a transitive call tree.
func RenderCallerDependencies(w io.Writer, g *graph.Graph) {
	if len(g.Dependencies) == 0 {
		return
	}

	// Group dependencies by Caller
	type callerGroup struct {
		caller     string
		callerType models.CallNodeType
		deps       []models.CallerDependency
	}

	groupMap := make(map[string]*callerGroup)
	var callerOrder []string

	for _, dep := range g.Dependencies {
		grp, exists := groupMap[dep.Caller]
		if !exists {
			grp = &callerGroup{
				caller:     dep.Caller,
				callerType: dep.CallerType,
			}
			groupMap[dep.Caller] = grp
			callerOrder = append(callerOrder, dep.Caller)
		}
		grp.deps = append(grp.deps, dep)
	}

	for _, callerName := range callerOrder {
		grp := groupMap[callerName]
		callerBadge := formatCallerBadge(callerName, grp.callerType)
		fmt.Fprintf(w, "\n%s%s%s\n", colorBold, callerBadge, colorReset)

		for i, dep := range grp.deps {
			isLast := i == len(grp.deps)-1
			renderDependencyTree(w, dep, "  ", isLast)
		}
	}
}

func renderDependencyTree(w io.Writer, dep models.CallerDependency, prefix string, isLast bool) {
	connector := "├── "
	childPrefix := prefix + "│   "
	if isLast {
		connector = "└── "
		childPrefix = prefix + "    "
	}

	calleeBadge := formatCalleeBadge(dep.CalleeType, dep.MCPServer)
	var statusStr string
	if dep.FailCount > 0 {
		statusStr = fmt.Sprintf("%d calls (%s✓ %d%s | %s✗ %d%s)",
			dep.CallCount,
			colorGreen, dep.SuccessCount, colorReset,
			colorRed, dep.FailCount, colorReset,
		)
	} else {
		statusStr = fmt.Sprintf("%d calls (%s✓ %d%s)",
			dep.CallCount,
			colorGreen, dep.SuccessCount, colorReset,
		)
	}

	fmt.Fprintf(w, "%s%s%s %s%s%s %s\n",
		prefix, connector, calleeBadge, colorBold, dep.Callee, colorReset, statusStr,
	)

	if dep.IsFallback {
		fallbackPrefix := childPrefix
		fmt.Fprintf(w, "%s%s⚠️  SILENT FALLBACK ➔ %s%s%s\n",
			fallbackPrefix, colorYellow, colorBold, dep.FallbackTo, colorReset,
		)
	}

	for j, child := range dep.Children {
		isChildLast := j == len(dep.Children)-1
		renderDependencyTree(w, child, childPrefix, isChildLast)
	}
}

func formatCallerBadge(name string, callerType models.CallNodeType) string {
	switch {
	case strings.HasPrefix(name, "Skill:"):
		return fmt.Sprintf("🧩 %s[Skill Caller]%s %s", colorGreen, colorReset, name)
	case strings.HasPrefix(name, "Subagent:"):
		return fmt.Sprintf("🤖 %s[Subagent Caller]%s %s", colorMagenta, colorReset, name)
	default:
		return fmt.Sprintf("🎯 %s[Agent Planner]%s %s", colorBlue, colorReset, name)
	}
}

func formatCalleeBadge(calleeType models.CallNodeType, mcpServer string) string {
	switch calleeType {
	case models.NodeTypeMCPTool:
		serverInfo := ""
		if mcpServer != "" {
			serverInfo = fmt.Sprintf(":%s", mcpServer)
		}
		return fmt.Sprintf("%s[MCP%s]%s", colorCyan, serverInfo, colorReset)
	case models.NodeTypeSkill:
		return fmt.Sprintf("%s[Skill]%s", colorGreen, colorReset)
	case models.NodeTypeSubagent:
		return fmt.Sprintf("%s[Subagent]%s", colorMagenta, colorReset)
	default:
		return fmt.Sprintf("%s[System]%s", colorDim, colorReset)
	}
}

// RenderExecutionTrace prints the turn-by-turn chronological execution trace.
func RenderExecutionTrace(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "\n%s📋 Turn-by-Turn Execution Trace: %s (%s)%s\n", colorBold, g.SessionID, g.Provider, colorReset)
	fmt.Fprintln(w, strings.Repeat("─", 65))

	if len(g.Roots) == 0 {
		fmt.Fprintln(w, "No tool calls recorded in this session.")
		return
	}

	visited := make(map[string]bool)
	for i, rootID := range g.Roots {
		isLast := i == len(g.Roots)-1
		renderNodeRecursive(w, g, rootID, "", isLast, visited)
	}
	fmt.Fprintln(w, strings.Repeat("─", 65))
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

	// Turn Root Node rendering
	if node.Type == models.NodeTypeUserTurn {
		fmt.Fprintf(w, "\n%s%s%s 💬 %s%s%s\n",
			prefix, connector, colorBold, colorCyan, node.Name, colorReset,
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
		return
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
