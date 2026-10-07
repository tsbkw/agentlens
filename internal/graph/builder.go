package graph

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
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
}

// GraphBuilder constructs a clean hierarchical Graph from turns and call nodes.
type GraphBuilder struct {
	nodes        map[string]*models.CallNode
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

	// Group nodes by Turn
	nodesByTurn := make(map[int][]*models.CallNode)
	for _, n := range b.nodes {
		nodesByTurn[n.TurnIndex] = append(nodesByTurn[n.TurnIndex], n)
	}

	for turnIdx := range nodesByTurn {
		sort.Slice(nodesByTurn[turnIdx], func(i, j int) bool {
			return nodesByTurn[turnIdx][i].Timestamp.Before(nodesByTurn[turnIdx][j].Timestamp)
		})
	}

	// If turns are provided, organize hierarchically by turn and scope
	if len(turns) > 0 {
		nodesByTurn := make(map[int][]*models.CallNode)
		for _, n := range b.nodes {
			nodesByTurn[n.TurnIndex] = append(nodesByTurn[n.TurnIndex], n)
		}

		for turnIdx := range nodesByTurn {
			sort.Slice(nodesByTurn[turnIdx], func(i, j int) bool {
				return nodesByTurn[turnIdx][i].Timestamp.Before(nodesByTurn[turnIdx][j].Timestamp)
			})
		}

		for _, turn := range turns {
			turnNodeID := fmt.Sprintf("turn-%d", turn.Index)
			turnLabel := turn.Prompt
			if len(turnLabel) > 60 {
				turnLabel = turnLabel[:60] + "..."
			}
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
			turnCalls := nodesByTurn[turn.Index]
			var lastFailedNode *models.CallNode

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
							if strings.HasPrefix(callerScope, "Skill:") {
								for prevScope, prevID := range scopeNodeMap {
									if strings.HasPrefix(prevScope, "Subagent") {
										scopeParentID = prevID
										break
									}
								}
							}
							b.AddEdge(scopeParentID, scopeID, models.EdgeTypeCalls)
							scopeNodeMap[callerScope] = scopeID
						}
						parentTarget = scopeID
					}
					b.AddEdge(parentTarget, callNode.ID, models.EdgeTypeCalls)
				}

				if lastFailedNode != nil {
					b.AddEdge(lastFailedNode.ID, callNode.ID, models.EdgeTypeFallbackTo)
					lastFailedNode = nil
				}

				if callNode.Status == models.StatusFailed {
					lastFailedNode = callNode
				}
			}
		}
	} else {
		// When no turns are provided (e.g. synthetic test cases or raw streams)
		sortedNodes := make([]*models.CallNode, 0, len(rawNodes))
		for _, n := range rawNodes {
			sortedNodes = append(sortedNodes, b.nodes[n.ID])
		}
		sort.Slice(sortedNodes, func(i, j int) bool {
			return sortedNodes[i].Timestamp.Before(sortedNodes[j].Timestamp)
		})

		// 1. Explicit parent-child hierarchy
		for _, node := range sortedNodes {
			if node.ParentID != "" && b.nodes[node.ParentID] != nil {
				b.AddEdge(node.ParentID, node.ID, models.EdgeTypeSpawns)
			}
		}

		// 2. Sequential fallback transition linking
		var prevNode *models.CallNode
		for _, node := range sortedNodes {
			if prevNode != nil {
				if prevNode.Status == models.StatusFailed {
					b.AddEdge(prevNode.ID, node.ID, models.EdgeTypeFallbackTo)
				}
			}
			prevNode = node
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
	}
}

func (b *GraphBuilder) computeDependencies(nodes []models.CallNode) []models.CallerDependency {
	type depKey struct {
		Caller string
		Callee string
	}

	depMap := make(map[depKey]*models.CallerDependency)
	scopeParentMap := make(map[string]string)

	for _, node := range nodes {
		caller := node.CallerScope
		if caller == "" {
			caller = "Agent (Main Planner)"
		}

		callee := node.Name
		calleeType := node.Type

		if node.Name == "invoke_subagent" {
			calleeType = models.NodeTypeSubagent
			if role, ok := node.Arguments["Role"].(string); ok && role != "" {
				callee = "Subagent: " + role
			} else if typeName, ok := node.Arguments["TypeName"].(string); ok && typeName != "" {
				callee = "Subagent: " + typeName
			}
			scopeParentMap[callee] = caller
		} else if node.Name == "view_file" {
			if absPath, ok := node.Arguments["AbsolutePath"].(string); ok {
				if strings.Contains(absPath, "/skills/") && strings.HasSuffix(absPath, "/SKILL.md") {
					parts := strings.Split(absPath, "/skills/")
					if len(parts) > 1 {
						skillName := strings.Split(parts[1], "/")[0]
						callee = "Skill: " + skillName
						calleeType = models.NodeTypeSkill
						scopeParentMap[callee] = caller
					}
				}
			}
		} else if strings.HasPrefix(node.Name, "skill_") {
			callee = "Skill: " + strings.TrimPrefix(node.Name, "skill_")
			calleeType = models.NodeTypeSkill
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
	for key, dep := range depMap {
		callerDeps[key.Caller] = append(callerDeps[key.Caller], dep)
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
	for key := range depMap {
		allCallees[key.Callee] = true
	}

	var rootCallers []string
	seenRoot := make(map[string]bool)

	// Primary root: Agent (Main Planner) or Agent
	for c := range callerDeps {
		if strings.HasPrefix(c, "Agent") {
			if !seenRoot[c] {
				rootCallers = append(rootCallers, c)
				seenRoot[c] = true
			}
		}
	}

	// Other callers that are not callees of anyone
	for c := range callerDeps {
		if !seenRoot[c] && !allCallees[c] {
			rootCallers = append(rootCallers, c)
			seenRoot[c] = true
		}
	}

	sort.Slice(rootCallers, func(i, j int) bool {
		if strings.HasPrefix(rootCallers[i], "Agent") {
			return true
		}
		if strings.HasPrefix(rootCallers[j], "Agent") {
			return false
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
	for caller, deps := range callerDeps {
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

		if node.Status == models.StatusFailed {
			m.FailedCalls++
		}
	}

	return m
}
