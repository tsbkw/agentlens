package graph

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// Graph represents the compiled hierarchical DAG and caller-to-callee dependencies.
type Graph struct {
	SessionID    string                       `json:"session_id"`
	Provider     string                       `json:"provider"`
	Turns        []models.ExecutionTurn       `json:"turns,omitempty"`
	Nodes        map[string]*models.CallNode  `json:"nodes"`
	Edges        []models.CallEdge            `json:"edges"`
	Roots        []string                     `json:"roots"`
	Children     map[string][]string          `json:"children"`
	Parents      map[string][]string          `json:"parents"`
	Dependencies []models.CallerDependency   `json:"dependencies"`

	// order lists node IDs in insertion order, used to break timestamp ties deterministically.
	order []string
}

// OrderedNodes returns all nodes sorted chronologically. Nodes sharing a timestamp (e.g. tool
// calls issued in one model response) keep the order in which they were added to the graph.
func (g *Graph) OrderedNodes() []*models.CallNode {
	nodes := make([]*models.CallNode, 0, len(g.Nodes))
	seen := make(map[string]bool, len(g.Nodes))
	for _, id := range g.order {
		if n := g.Nodes[id]; n != nil && !seen[id] {
			nodes = append(nodes, n)
			seen[id] = true
		}
	}
	// Graphs not produced by the builder have no recorded order; fall back to ID order.
	var rest []*models.CallNode
	for id, n := range g.Nodes {
		if !seen[id] {
			rest = append(rest, n)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].ID < rest[j].ID })
	nodes = append(nodes, rest...)

	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].Timestamp.Before(nodes[j].Timestamp)
	})
	return nodes
}

// GraphBuilder constructs a clean hierarchical Graph from turns and call nodes.
type GraphBuilder struct {
	nodes        map[string]*models.CallNode
	order        []string
	edges        []models.CallEdge
	children     map[string][]string
	parents      map[string][]string
	roots        []string
	dependencies []models.CallerDependency
}

// NewGraphBuilder creates an empty GraphBuilder.
func NewGraphBuilder() *GraphBuilder {
	return &GraphBuilder{
		nodes:        make(map[string]*models.CallNode),
		edges:        make([]models.CallEdge, 0),
		children:     make(map[string][]string),
		parents:      make(map[string][]string),
		roots:        make([]string, 0),
		dependencies: make([]models.CallerDependency, 0),
	}
}

// AddNode registers a node in the graph.
func (b *GraphBuilder) AddNode(node models.CallNode) {
	if _, exists := b.nodes[node.ID]; !exists {
		b.order = append(b.order, node.ID)
	}
	b.nodes[node.ID] = &node
}

// AddEdge creates a directed relationship from source to target.
func (b *GraphBuilder) AddEdge(sourceID, targetID string, edgeType models.CallEdgeType) {
	for _, e := range b.edges {
		if e.SourceID == sourceID && e.TargetID == targetID && e.Type == edgeType {
			return
		}
	}

	b.edges = append(b.edges, models.CallEdge{
		SourceID: sourceID,
		TargetID: targetID,
		Type:     edgeType,
	})

	b.children[sourceID] = append(b.children[sourceID], targetID)
	b.parents[targetID] = append(b.parents[targetID], sourceID)
}

// Build constructs the hierarchical DAG from turns and raw call nodes.
func (b *GraphBuilder) Build(sessionID, providerID string, rawNodes []models.CallNode) *Graph {
	return b.BuildWithTurns(sessionID, providerID, nil, rawNodes)
}

// BuildWithTurns constructs the hierarchical DAG organized by conversational turns.
func (b *GraphBuilder) BuildWithTurns(
	sessionID, providerID string,
	turns []models.ExecutionTurn,
	rawNodes []models.CallNode,
) *Graph {
	for _, n := range rawNodes {
		b.AddNode(n)
	}

	// Call nodes in chronological order; ties keep their input order
	sortedNodes := b.sortedNodes()

	// If turns are provided, organize hierarchically by turn and scope
	if len(turns) > 0 {
		nodesByTurn := make(map[int][]*models.CallNode)
		for _, n := range sortedNodes {
			nodesByTurn[n.TurnIndex] = append(nodesByTurn[n.TurnIndex], n)
		}

		for _, turn := range turns {
			turnNodeID := fmt.Sprintf("turn-%d", turn.Index)
			turnLabel := textutil.Truncate(turn.Prompt, 60, "...")
			if turnLabel == "" {
				turnLabel = fmt.Sprintf("Turn %d", turn.Index)
			}

			turnNode := models.CallNode{
				ID:        turnNodeID,
				SessionID: sessionID,
				TurnIndex: turn.Index,
				Type:      models.NodeTypeUserTurn,
				Name:      fmt.Sprintf("Turn %d: %s", turn.Index, turnLabel),
				Timestamp: turn.Timestamp,
				Status:    models.StatusSuccess,
			}
			b.AddNode(turnNode)
			b.roots = append(b.roots, turnNodeID)

			scopeNodeMap := make(map[string]string)
			// Skills invoked after a subagent scope opened are nested under the latest one
			lastSubagentScopeID := ""
			turnCalls := nodesByTurn[turn.Index]
			// Fallback transitions are tracked per actor: calls of a subagent (sharing a
			// ParentID) can interleave in time with the calls of the agent that spawned it.
			lastFailedByActor := make(map[string]*models.CallNode)

			for _, callNode := range turnCalls {
				if callNode.ParentID != "" && b.nodes[callNode.ParentID] != nil {
					b.AddEdge(callNode.ParentID, callNode.ID, models.EdgeTypeSpawns)
				} else {
					parentTarget := turnNodeID
					callerScope := callNode.CallerScope
					if callerScope != "" && callerScope != "Agent" {
						scopeID, exists := scopeNodeMap[callerScope]
						if !exists {
							scopeID = fmt.Sprintf("scope-%d-%s", turn.Index, sanitizeID(callerScope))
							scopeType := models.NodeTypeSkill
							if strings.HasPrefix(callerScope, "Subagent") {
								scopeType = models.NodeTypeSubagent
							}

							scopeNode := models.CallNode{
								ID:        scopeID,
								SessionID: sessionID,
								TurnIndex: turn.Index,
								Type:      scopeType,
								Name:      callerScope,
								Timestamp: callNode.Timestamp,
								Status:    models.StatusSuccess,
							}
							b.AddNode(scopeNode)
							scopeParentID := turnNodeID
							if strings.HasPrefix(callerScope, "Skill:") && lastSubagentScopeID != "" {
								scopeParentID = lastSubagentScopeID
							}
							b.AddEdge(scopeParentID, scopeID, models.EdgeTypeCalls)
							scopeNodeMap[callerScope] = scopeID
							if scopeType == models.NodeTypeSubagent {
								lastSubagentScopeID = scopeID
							}
						}
						parentTarget = scopeID
					}
					b.AddEdge(parentTarget, callNode.ID, models.EdgeTypeCalls)
				}

				if !takesPartInAttempts(callNode) {
					continue
				}
				if lastFailed := lastFailedByActor[callNode.ParentID]; lastFailed != nil {
					b.AddEdge(lastFailed.ID, callNode.ID, transitionAfterFailure(lastFailed, callNode))
					delete(lastFailedByActor, callNode.ParentID)
				}

				if callNode.Status == models.StatusFailed {
					lastFailedByActor[callNode.ParentID] = callNode
				}
			}
		}
	} else {
		// When no turns are provided (e.g. synthetic test cases or raw streams)
		// 1. Explicit parent-child hierarchy
		for _, node := range sortedNodes {
			if node.ParentID != "" && b.nodes[node.ParentID] != nil {
				b.AddEdge(node.ParentID, node.ID, models.EdgeTypeSpawns)
			}
		}

		// 2. Sequential fallback transition linking (per actor, see BuildWithTurns)
		prevByActor := make(map[string]*models.CallNode)
		for _, node := range sortedNodes {
			if !takesPartInAttempts(node) {
				continue
			}
			if prevNode := prevByActor[node.ParentID]; prevNode != nil {
				if prevNode.Status == models.StatusFailed {
					b.AddEdge(prevNode.ID, node.ID, transitionAfterFailure(prevNode, node))
				}
			}
			prevByActor[node.ParentID] = node
		}

		// Roots are nodes without parents
		for _, node := range sortedNodes {
			if len(b.parents[node.ID]) == 0 {
				b.roots = append(b.roots, node.ID)
			}
		}
	}

	// 2. Compute Caller -> Callee Dependencies (Transitive Call Tree)
	b.dependencies = b.computeDependencies(rawNodes)

	return &Graph{
		SessionID:    sessionID,
		Provider:     providerID,
		Turns:        turns,
		Nodes:        b.nodes,
		Edges:        b.edges,
		Roots:        b.roots,
		Children:     b.children,
		Parents:      b.parents,
		Dependencies: b.dependencies,
		order:        b.order,
	}
}

// sortedNodes returns the registered nodes chronologically, keeping insertion order for ties.
func (b *GraphBuilder) sortedNodes() []*models.CallNode {
	nodes := make([]*models.CallNode, 0, len(b.order))
	for _, id := range b.order {
		nodes = append(nodes, b.nodes[id])
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].Timestamp.Before(nodes[j].Timestamp)
	})
	return nodes
}

func (b *GraphBuilder) computeDependencies(nodes []models.CallNode) []models.CallerDependency {
	type depKey struct {
		Caller string
		Callee string
	}

	depMap := make(map[depKey]*models.CallerDependency)
	var depOrder []depKey // first-seen order, so map iteration never decides the output order
	scopeParentMap := make(map[string]string)

	for _, node := range nodes {
		caller := node.CallerScope
		if caller == "" {
			caller = "Agent (Main Planner)"
		}

		callee := node.Name
		calleeType := node.Type

		if node.ScopeName != "" {
			callee = node.ScopeName
			scopeParentMap[callee] = caller
		}

		key := depKey{Caller: caller, Callee: callee}
		dep, exists := depMap[key]
		if !exists {
			callerType := models.NodeTypeAgent
			if strings.HasPrefix(caller, "Skill:") {
				callerType = models.NodeTypeSkill
			} else if strings.HasPrefix(caller, "Subagent:") {
				callerType = models.NodeTypeSubagent
			}

			dep = &models.CallerDependency{
				Caller:     caller,
				CallerType: callerType,
				Callee:     callee,
				CalleeType: calleeType,
				MCPServer:  node.MCPServer,
			}
			depMap[key] = dep
			depOrder = append(depOrder, key)
		}

		if node.IsInferredPlaceholder() {
			dep.Inferred = true
		}
		dep.CallCount++
		if node.Status == models.StatusSuccess {
			dep.SuccessCount++
		} else if node.Status == models.StatusFailed {
			dep.FailCount++
		}
	}

	// Detect fallback edges for dependencies
	for _, edge := range b.edges {
		if edge.Type == models.EdgeTypeFallbackTo {
			srcNode := b.nodes[edge.SourceID]
			tgtNode := b.nodes[edge.TargetID]
			if srcNode != nil && tgtNode != nil {
				caller := srcNode.CallerScope
				if caller == "" {
					caller = "Agent (Main Planner)"
				}
				key := depKey{Caller: caller, Callee: srcNode.Name}
				if dep, exists := depMap[key]; exists {
					dep.IsFallback = true
					dep.FallbackTo = tgtNode.Name
				}
			}
		}
	}

	// Group dependencies by Caller
	callerDeps := make(map[string][]*models.CallerDependency)
	var callerOrder []string
	for _, key := range depOrder {
		if _, exists := callerDeps[key.Caller]; !exists {
			callerOrder = append(callerOrder, key.Caller)
		}
		callerDeps[key.Caller] = append(callerDeps[key.Caller], depMap[key])
	}

	for c := range callerDeps {
		sort.Slice(callerDeps[c], func(i, j int) bool {
			if callerDeps[c][i].CallCount != callerDeps[c][j].CallCount {
				return callerDeps[c][i].CallCount > callerDeps[c][j].CallCount
			}
			return callerDeps[c][i].Callee < callerDeps[c][j].Callee
		})
	}

	// Attach children transitively to callees that are themselves callers
	visitedScopes := make(map[string]bool)

	var attachChildren func(dep *models.CallerDependency)
	attachChildren = func(dep *models.CallerDependency) {
		childScope := dep.Callee
		if visitedScopes[childScope] {
			return
		}
		visitedScopes[childScope] = true

		if subCalls, exists := callerDeps[childScope]; exists {
			for _, sub := range subCalls {
				subCopy := *sub
				attachChildren(&subCopy)
				dep.Children = append(dep.Children, subCopy)
			}
		}
	}

	allCallees := make(map[string]bool)
	for _, key := range depOrder {
		allCallees[key.Callee] = true
	}

	var rootCallers []string
	seenRoot := make(map[string]bool)

	// Primary root: Agent (Main Planner) or Agent
	for _, c := range callerOrder {
		if strings.HasPrefix(c, "Agent") {
			if !seenRoot[c] {
				rootCallers = append(rootCallers, c)
				seenRoot[c] = true
			}
		}
	}

	// Other callers that are not callees of anyone
	for _, c := range callerOrder {
		if !seenRoot[c] && !allCallees[c] {
			rootCallers = append(rootCallers, c)
			seenRoot[c] = true
		}
	}

	sort.Slice(rootCallers, func(i, j int) bool {
		iAgent, jAgent := strings.HasPrefix(rootCallers[i], "Agent"), strings.HasPrefix(rootCallers[j], "Agent")
		if iAgent != jAgent {
			return iAgent
		}
		return rootCallers[i] < rootCallers[j]
	})

	var result []models.CallerDependency
	for _, rootCaller := range rootCallers {
		for _, dep := range callerDeps[rootCaller] {
			depCopy := *dep
			attachChildren(&depCopy)
			result = append(result, depCopy)
		}
	}

	// If there are orphaned caller scopes that were not attached (e.g. synthetic test nodes)
	for _, caller := range callerOrder {
		deps := callerDeps[caller]
		if !visitedScopes[caller] && !seenRoot[caller] {
			var targetRoot string
			for _, r := range rootCallers {
				if strings.HasPrefix(r, "Agent") {
					targetRoot = r
					break
				}
			}

			callerType := models.NodeTypeSkill
			if strings.HasPrefix(caller, "Subagent") {
				callerType = models.NodeTypeSubagent
			}

			if targetRoot != "" {
				syntheticDep := models.CallerDependency{
					Caller:       targetRoot,
					CallerType:   models.NodeTypeAgent,
					Callee:       caller,
					CalleeType:   callerType,
					CallCount:    1,
					SuccessCount: 1,
				}
				for _, d := range deps {
					dCopy := *d
					attachChildren(&dCopy)
					syntheticDep.Children = append(syntheticDep.Children, dCopy)
				}
				result = append(result, syntheticDep)
			} else {
				for _, d := range deps {
					dCopy := *d
					attachChildren(&dCopy)
					result = append(result, dCopy)
				}
			}
			visitedScopes[caller] = true
		}
	}

	return result
}

// takesPartInAttempts reports whether a call counts in a sequence of attempts at the user's
// task. Bookkeeping calls and calls that were denied before running are skipped, so that
// "MCP failed -> tool search -> shell" still links the failed MCP call to the shell call.
func takesPartInAttempts(node *models.CallNode) bool {
	return !node.Auxiliary && node.Status != models.StatusDenied
}

// transitionAfterFailure classifies the edge from a failed call to the call that follows it:
// re-running the same tool is a retry, switching to another tool is a fallback candidate.
func transitionAfterFailure(failed, next *models.CallNode) models.CallEdgeType {
	if failed.Name == next.Name {
		return models.EdgeTypeRetries
	}
	return models.EdgeTypeFallbackTo
}

// ApplyConfirmedFallbacks resets the fallback flags of the dependency tree and sets them only
// for the given silent-fallback anomalies (RelatedNodeID -> NodeID), e.g. those confirmed by
// the anomaly detector. When one caller -> callee row has several, a high-confidence one
// wins, then the first in order.
func (g *Graph) ApplyConfirmedFallbacks(fallbacks []models.AnomalyRecord) {
	type depKey struct{ Caller, Callee string }
	type mark struct {
		to         string
		confidence models.Confidence
	}
	marks := make(map[depKey]mark)
	for _, f := range fallbacks {
		src, tgt := g.Nodes[f.RelatedNodeID], g.Nodes[f.NodeID]
		if src == nil || tgt == nil {
			continue
		}
		caller := src.CallerScope
		if caller == "" {
			caller = "Agent (Main Planner)"
		}
		key := depKey{Caller: caller, Callee: src.Name}
		if prev, exists := marks[key]; !exists || (prev.confidence != models.ConfidenceHigh && f.Confidence == models.ConfidenceHigh) {
			marks[key] = mark{to: tgt.Name, confidence: f.Confidence}
		}
	}

	var walk func(deps []models.CallerDependency)
	walk = func(deps []models.CallerDependency) {
		for i := range deps {
			m, ok := marks[depKey{Caller: deps[i].Caller, Callee: deps[i].Callee}]
			deps[i].IsFallback = ok
			deps[i].FallbackTo = m.to
			deps[i].FallbackConfidence = m.confidence
			walk(deps[i].Children)
		}
	}
	walk(g.Dependencies)
}

func sanitizeID(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

// GraphMetrics provides statistical summaries of a Graph.
type GraphMetrics struct {
	TotalNodes    int           `json:"total_nodes"`
	TotalEdges    int           `json:"total_edges"`
	SkillCalls    int           `json:"skill_calls"`
	MCPToolCalls  int           `json:"mcp_tool_calls"`
	SystemCalls   int           `json:"system_calls"`
	FailedCalls   int           `json:"failed_calls"`
	DeniedCalls   int           `json:"denied_calls"`
	MaxDepth      int           `json:"max_depth"`
	TotalDuration time.Duration `json:"total_duration"`
}

// ComputeMetrics calculates execution summary metrics for the Graph.
func (g *Graph) ComputeMetrics() GraphMetrics {
	var m GraphMetrics
	m.TotalNodes = len(g.Nodes)
	m.TotalEdges = len(g.Edges)

	for _, node := range g.Nodes {
		switch node.Type {
		case models.NodeTypeSkill:
			m.SkillCalls++
		case models.NodeTypeMCPTool:
			m.MCPToolCalls++
		case models.NodeTypeSystemTool:
			m.SystemCalls++
		}

		switch node.Status {
		case models.StatusFailed:
			m.FailedCalls++
		case models.StatusDenied:
			m.DeniedCalls++
		}
	}

	return m
}
