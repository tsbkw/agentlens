package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tsbkw/agentlens/internal/ansi"
	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// RenderSessionList writes a formatted table of discovered sessions.
func RenderSessionList(w io.Writer, sessions []collector.SessionInfo) {
	if len(sessions) == 0 {
		fmt.Fprintln(w, "No recorded agent sessions found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "%sSESSION ID\tPROVIDER\tMODIFIED\tSIZE\tPATH%s\n", ansi.Bold, ansi.Reset)

	for _, s := range sessions {
		// Display shorter session ID if long
		displayID := textutil.Truncate(s.SessionID, 24, "...")

		timeStr := s.ModTime.Format("2006-01-02 15:04:05")
		sizeStr := textutil.FormatSize(s.SizeBytes)

		fmt.Fprintf(tw, "%s%s%s\t%s\t%s\t%s\t%s%s%s\n",
			ansi.Cyan, displayID, ansi.Reset,
			s.ProviderID,
			timeStr,
			sizeStr,
			ansi.Dim, s.FilePath, ansi.Reset,
		)
	}
	tw.Flush()
	fmt.Fprintf(w, "\nFound %d session(s). Use `agentlens graph <session-id>` to inspect call flow.\n", len(sessions))
}

// RenderCallGraph prints the caller-to-callee dependency call graph and session overview.
func RenderCallGraph(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "\n%s🔍 Caller ➔ Callee Call Graph: %s (%s)%s\n", ansi.Bold, g.SessionID, g.Provider, ansi.Reset)
	fmt.Fprintf(w, "%sTraces where each Skill, Subagent, and MCP tool was invoked from.%s\n", ansi.Dim, ansi.Reset)
	fmt.Fprintln(w, strings.Repeat("─", 65))

	if len(g.Dependencies) == 0 && len(g.Nodes) == 0 {
		fmt.Fprintln(w, "No tool calls recorded in this session.")
		return
	}

	RenderCallerDependencies(w, g)

	// Print summary metrics
	metrics := g.ComputeMetrics()
	fmt.Fprintln(w, strings.Repeat("─", 65))
	fmt.Fprintf(w, "%sSummary:%s Nodes: %d | Edges: %d | MCP Calls: %d | Skill Calls: %d | System Calls: %d | Failed: %d | Denied: %d\n",
		ansi.Bold, ansi.Reset,
		metrics.TotalNodes, metrics.TotalEdges,
		metrics.MCPToolCalls, metrics.SkillCalls, metrics.SystemCalls, metrics.FailedCalls, metrics.DeniedCalls,
	)

	// Anomaly highlights
	var allAnomalies []models.AnomalyRecord
	for _, n := range g.OrderedNodes() {
		allAnomalies = append(allAnomalies, n.Anomalies...)
	}

	if len(allAnomalies) > 0 {
		fmt.Fprintf(w, "\n%s⚠️  Detected %d Anomaly Alert(s):%s\n", ansi.Yellow, len(allAnomalies), ansi.Reset)
		// The same finding on repeated calls (such as every retry of one MCP tool) is listed once
		for _, grp := range groupAnomalies(allAnomalies) {
			a := grp.first
			badge := fmt.Sprintf("%s[%s]%s", ansi.Yellow, a.Severity, ansi.Reset)
			if a.Severity == models.SeverityCritical {
				badge = fmt.Sprintf("%s[%s]%s", ansi.Red, a.Severity, ansi.Reset)
			}
			times := ""
			if grp.count > 1 {
				times = fmt.Sprintf(" %s×%d%s", ansi.Dim, grp.count, ansi.Reset)
			}
			fmt.Fprintf(w, "  • %s %s%s%s%s: %s\n", badge, ansi.Bold, a.Title, ansi.Reset, times, a.Description)
			if a.Recommendation != "" {
				fmt.Fprintf(w, "    %s💡 Fix:%s %s\n", ansi.Cyan, ansi.Reset, a.Recommendation)
			}
		}
	}

	fmt.Fprintf(w, "\n%s💡 Tip:%s Use `agentlens trace %s` to view the turn-by-turn chronological execution trace.\n\n",
		ansi.Cyan, ansi.Reset, g.SessionID,
	)
}

type anomalyGroup struct {
	first models.AnomalyRecord
	count int
}

// groupAnomalies merges records of the same type and title, keeping their first order.
func groupAnomalies(records []models.AnomalyRecord) []anomalyGroup {
	var groups []anomalyGroup
	index := map[string]int{}
	for _, a := range records {
		key := string(a.Type) + "\x00" + a.Title
		if i, ok := index[key]; ok {
			groups[i].count++
			continue
		}
		index[key] = len(groups)
		groups = append(groups, anomalyGroup{first: a, count: 1})
	}
	return groups
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
		fmt.Fprintf(w, "\n%s%s%s\n", ansi.Bold, callerBadge, ansi.Reset)

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
			ansi.Green, dep.SuccessCount, ansi.Reset,
			ansi.Red, dep.FailCount, ansi.Reset,
		)
	} else {
		statusStr = fmt.Sprintf("%d calls (%s✓ %d%s)",
			dep.CallCount,
			ansi.Green, dep.SuccessCount, ansi.Reset,
		)
	}

	inferredNote := ""
	if dep.Inferred {
		inferredNote = fmt.Sprintf(" %s(inferred: spawning call not recorded)%s", ansi.Yellow, ansi.Reset)
	}
	fmt.Fprintf(w, "%s%s%s %s%s%s %s%s\n",
		prefix, connector, calleeBadge, ansi.Bold, dep.Callee, ansi.Reset, statusStr, inferredNote,
	)

	if dep.IsFallback {
		label := "SILENT FALLBACK"
		if dep.FallbackConfidence == models.ConfidenceLow {
			label = "POSSIBLE FALLBACK (low confidence)"
		}
		fmt.Fprintf(w, "%s%s⚠️  %s ➔ %s%s%s\n",
			childPrefix, ansi.Yellow, label, ansi.Bold, dep.FallbackTo, ansi.Reset,
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
		return fmt.Sprintf("🧩 %s[Skill Caller]%s %s", ansi.Green, ansi.Reset, name)
	case strings.HasPrefix(name, "Subagent:"):
		return fmt.Sprintf("🤖 %s[Subagent Caller]%s %s", ansi.Magenta, ansi.Reset, name)
	default:
		return fmt.Sprintf("🎯 %s[Agent Planner]%s %s", ansi.Blue, ansi.Reset, name)
	}
}

func formatCalleeBadge(calleeType models.CallNodeType, mcpServer string) string {
	switch calleeType {
	case models.NodeTypeMCPTool:
		serverInfo := ""
		if mcpServer != "" {
			serverInfo = fmt.Sprintf(":%s", mcpServer)
		}
		return fmt.Sprintf("%s[MCP%s]%s", ansi.Cyan, serverInfo, ansi.Reset)
	case models.NodeTypeSkill:
		return fmt.Sprintf("%s[Skill]%s", ansi.Green, ansi.Reset)
	case models.NodeTypeSubagent:
		return fmt.Sprintf("%s[Subagent]%s", ansi.Magenta, ansi.Reset)
	default:
		return fmt.Sprintf("%s[System]%s", ansi.Dim, ansi.Reset)
	}
}

// RenderExecutionTrace prints the turn-by-turn chronological execution trace.
func RenderExecutionTrace(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "\n%s📋 Turn-by-Turn Execution Trace: %s (%s)%s\n", ansi.Bold, g.SessionID, g.Provider, ansi.Reset)
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
			prefix, connector, ansi.Bold, ansi.Cyan, node.Name, ansi.Reset,
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
	statusBadge := fmt.Sprintf("%s✓%s", ansi.Green, ansi.Reset)
	if node.Status == models.StatusFailed {
		statusBadge = fmt.Sprintf("%s✗%s", ansi.Red, ansi.Reset)
	} else if node.Status == models.StatusTimeout {
		statusBadge = fmt.Sprintf("%s⏱%s", ansi.Yellow, ansi.Reset)
	} else if node.Status == models.StatusDenied {
		statusBadge = fmt.Sprintf("%s⊘%s", ansi.Yellow, ansi.Reset)
	}

	// Type badge
	typeColor := ansi.Blue
	switch node.Type {
	case models.NodeTypeMCPTool:
		typeColor = ansi.Cyan
	case models.NodeTypeSkill:
		typeColor = ansi.Green
	case models.NodeTypeSubagent:
		typeColor = ansi.Magenta
	}
	typeBadge := fmt.Sprintf("%s[%s]%s", typeColor, node.Type, ansi.Reset)

	// Anomaly alerts
	anomalyBadge := ""
	for _, anom := range node.Anomalies {
		if anom.Type == models.AnomalyAuthExpired {
			anomalyBadge += fmt.Sprintf(" %s🚨 AUTH EXPIRED%s", ansi.Red, ansi.Reset)
		} else if anom.Type == models.AnomalySilentFallback {
			anomalyBadge += fmt.Sprintf(" %s⚠️ SILENT FALLBACK%s", ansi.Yellow, ansi.Reset)
		} else if anom.Type == models.AnomalyRetryLoop {
			anomalyBadge += fmt.Sprintf(" %s🔁 RETRY LOOP%s", ansi.Yellow, ansi.Reset)
		}
	}

	mcpInfo := ""
	if node.MCPServer != "" {
		mcpInfo = fmt.Sprintf(" %s(server: %s)%s", ansi.Dim, node.MCPServer, ansi.Reset)
	}
	if node.DurationMs > 0 {
		mcpInfo += fmt.Sprintf(" %s%s%s", ansi.Dim, textutil.FormatDuration(node.DurationMs), ansi.Reset)
	}
	name := node.Name
	if node.IsInferredPlaceholder() {
		name = node.ScopeName
		mcpInfo += fmt.Sprintf(" %s(inferred: spawning call not recorded, placed by time)%s", ansi.Yellow, ansi.Reset)
	}

	fmt.Fprintf(w, "%s%s%s %s %s%s%s%s%s\n",
		prefix, connector, statusBadge, typeBadge, ansi.Bold, name, ansi.Reset, mcpInfo, anomalyBadge,
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
	fmt.Fprintf(w, "\n%s🔍 Call Node Detail: %s%s\n", ansi.Bold, node.ID, ansi.Reset)
	fmt.Fprintln(w, strings.Repeat("─", 60))

	fmt.Fprintf(w, "  %sName:%s        %s\n", ansi.Bold, ansi.Reset, node.Name)
	fmt.Fprintf(w, "  %sType:%s        %s\n", ansi.Bold, ansi.Reset, node.Type)
	if node.MCPServer != "" {
		fmt.Fprintf(w, "  %sMCP Server:%s  %s\n", ansi.Bold, ansi.Reset, node.MCPServer)
	}
	fmt.Fprintf(w, "  %sSession:%s     %s\n", ansi.Bold, ansi.Reset, node.SessionID)
	if node.ParentID != "" {
		fmt.Fprintf(w, "  %sParent ID:%s   %s\n", ansi.Bold, ansi.Reset, node.ParentID)
	}
	if node.ParentLink != "" {
		fmt.Fprintf(w, "  %sParent link:%s %s\n", ansi.Bold, ansi.Reset, describeParentLink(node.ParentLink))
	}
	fmt.Fprintf(w, "  %sTimestamp:%s   %s\n", ansi.Bold, ansi.Reset, node.Timestamp.Format("2006-01-02 15:04:05.000"))
	if node.DurationMs > 0 {
		fmt.Fprintf(w, "  %sDuration:%s    %s\n", ansi.Bold, ansi.Reset, textutil.FormatDuration(node.DurationMs))
	}
	fmt.Fprintf(w, "  %sStatus:%s      %s\n", ansi.Bold, ansi.Reset, node.Status)

	if node.ErrorMessage != "" {
		fmt.Fprintf(w, "  %sError:%s       %s%s%s\n", ansi.Bold, ansi.Reset, ansi.Red, node.ErrorMessage, ansi.Reset)
	}

	// Print arguments as indented JSON
	if len(node.Arguments) > 0 {
		fmt.Fprintf(w, "\n%sArguments:%s\n", ansi.Bold, ansi.Reset)
		if argsJSON, err := json.MarshalIndent(node.Arguments, "  ", "  "); err == nil {
			fmt.Fprintf(w, "  %s\n", string(argsJSON))
		}
	}

	// Print output if present
	if node.Output != nil {
		fmt.Fprintf(w, "\n%sOutput:%s\n", ansi.Bold, ansi.Reset)
		outStr := textutil.Truncate(fmt.Sprintf("%v", node.Output), 500, "... (truncated)")
		fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(outStr, "\n", "\n  "))
	}

	// Print anomalies if any
	if len(node.Anomalies) > 0 {
		fmt.Fprintf(w, "\n%sDetected Anomalies & Alerts:%s\n", ansi.Bold, ansi.Reset)
		for _, anom := range node.Anomalies {
			sevColor := ansi.Yellow
			if anom.Severity == models.SeverityCritical {
				sevColor = ansi.Red
			}
			fmt.Fprintf(w, "  • %s[%s] %s%s\n", sevColor, anom.Severity, anom.Title, ansi.Reset)
			fmt.Fprintf(w, "    %s\n", anom.Description)
			if anom.Recommendation != "" {
				fmt.Fprintf(w, "    %sRecommendation:%s %s\n", ansi.Cyan, ansi.Reset, anom.Recommendation)
			}
		}
	}
	fmt.Fprintln(w, strings.Repeat("─", 60))
}

func describeParentLink(link models.ParentLink) string {
	switch link {
	case models.ParentLinkSidecar:
		return "sidecar (the subagent's metadata names the spawning call)"
	case models.ParentLinkResult:
		return "result (the spawning call's result names the subagent)"
	case models.ParentLinkInferred:
		return fmt.Sprintf("%sinferred%s (no record names the spawning call; placed in the turn active when the subagent started)", ansi.Yellow, ansi.Reset)
	default:
		return string(link)
	}
}
