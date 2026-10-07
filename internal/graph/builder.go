package graph

import (
	"sort"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
)

// Graph represents a compiled Directed Acyclic Graph (DAG) of an execution session.
type Graph struct {
	SessionID string                       `json:"session_id"`
	Provider  string                       `json:"provider"`
	Nodes     map[string]*models.CallNode  `json:"nodes"`
	Edges     []models.CallEdge            `json:"edges"`
	Roots     []string                     `json:"roots"` // Node IDs with no parents
	Children  map[string][]string          `json:"children"`
	Parents   map[string][]string          `json:"parents"`
}

// GraphBuilder constructs a Graph from an unordered list of CallNodes.
type GraphBuilder struct {
	nodes    map[string]*models.CallNode
	edges    []models.CallEdge
	children map[string][]string
	parents  map[string][]string
}

// NewGraphBuilder creates an empty GraphBuilder.
func NewGraphBuilder() *GraphBuilder {
	return &GraphBuilder{
		nodes:    make(map[string]*models.CallNode),
		edges:    make([]models.CallEdge, 0),
		children: make(map[string][]string),
		parents:  make(map[string][]string),
	}
}

// AddNode adds a CallNode to the graph.
func (b *GraphBuilder) AddNode(node models.CallNode) {
	b.nodes[node.ID] = &node
}

// AddEdge creates a directed relationship from source to target.
func (b *GraphBuilder) AddEdge(sourceID, targetID string, edgeType models.CallEdgeType) {
	// Avoid duplicate edges
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

// Build constructs and returns the final Graph.
func (b *GraphBuilder) Build(sessionID, providerID string, rawNodes []models.CallNode) *Graph {
	for _, n := range rawNodes {
		b.AddNode(n)
	}

	// Sort nodes by timestamp to establish chronological order
	sortedNodes := make([]*models.CallNode, 0, len(b.nodes))
	for _, n := range b.nodes {
		sortedNodes = append(sortedNodes, n)
	}
	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].Timestamp.Before(sortedNodes[j].Timestamp)
	})

	// 1. Explicit parent-child connections
	for _, node := range sortedNodes {
		if node.ParentID != "" && node.ParentID != node.ID {
			if _, exists := b.nodes[node.ParentID]; exists {
				edgeType := models.EdgeTypeCalls
				if b.nodes[node.ParentID].Type == models.NodeTypeSubagent {
					edgeType = models.EdgeTypeSpawns
				}
				b.AddEdge(node.ParentID, node.ID, edgeType)
			}
		}
	}

	// 2. Subagent spawning detection
	for i, node := range sortedNodes {
		if node.Type == models.NodeTypeSubagent {
			// Look ahead for tool calls executed within this subagent
			for j := i + 1; j < len(sortedNodes); j++ {
				next := sortedNodes[j]
				if next.ParentID == node.ID || next.ParentID == "" {
					b.AddEdge(node.ID, next.ID, models.EdgeTypeSpawns)
					break
				}
			}
		}
	}

	// 3. Sequential flow linking between consecutive root-level nodes
	var prevRoot *models.CallNode
	for _, node := range sortedNodes {
		if len(b.parents[node.ID]) == 0 {
			if prevRoot != nil {
				// Link previous call to next call
				edgeType := models.EdgeTypeCalls
				if prevRoot.Status == models.StatusFailed {
					edgeType = models.EdgeTypeFallbackTo
				}
				b.AddEdge(prevRoot.ID, node.ID, edgeType)
			}
			prevRoot = node
		}
	}

	// 4. Determine root nodes
	var roots []string
	for _, node := range sortedNodes {
		if len(b.parents[node.ID]) == 0 {
			roots = append(roots, node.ID)
		}
	}

	return &Graph{
		SessionID: sessionID,
		Provider:  providerID,
		Nodes:     b.nodes,
		Edges:     b.edges,
		Roots:     roots,
		Children:  b.children,
		Parents:   b.parents,
	}
}

// GraphMetrics provides statistical summaries of a Graph.
type GraphMetrics struct {
	TotalNodes     int           `json:"total_nodes"`
	TotalEdges     int           `json:"total_edges"`
	SkillCalls     int           `json:"skill_calls"`
	MCPToolCalls   int           `json:"mcp_tool_calls"`
	SystemCalls    int           `json:"system_calls"`
	FailedCalls    int           `json:"failed_calls"`
	MaxDepth       int           `json:"max_depth"`
	TotalDuration  time.Duration `json:"total_duration"`
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

	m.MaxDepth = g.calculateMaxDepth()
	return m
}

func (g *Graph) calculateMaxDepth() int {
	maxDepth := 0
	visited := make(map[string]bool)

	var dfs func(id string, depth int)
	dfs = func(id string, depth int) {
		if depth > maxDepth {
			maxDepth = depth
		}
		if visited[id] {
			return
		}
		visited[id] = true
		for _, childID := range g.Children[id] {
			dfs(childID, depth+1)
		}
	}

	for _, rootID := range g.Roots {
		dfs(rootID, 1)
	}

	return maxDepth
}
