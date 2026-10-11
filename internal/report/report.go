// Package report aggregates the calls and anomalies of many sessions over a time window.
package report

import (
	"sort"
	"time"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
)

// Window is a time range [Since, Until). A zero bound is open.
type Window struct {
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
}

// Contains reports whether t falls in the window.
func (w Window) Contains(t time.Time) bool {
	return (w.Since.IsZero() || !t.Before(w.Since)) && (w.Until.IsZero() || t.Before(w.Until))
}

// Counts summarizes calls and anomalies.
type Counts struct {
	Calls             int `json:"calls"`
	Failed            int `json:"failed"`
	Denied            int `json:"denied"`
	AuthExpired       int `json:"auth_expired"`
	SilentFallbacks   int `json:"silent_fallbacks"`
	PossibleFallbacks int `json:"possible_fallbacks"` // low-confidence silent fallbacks
	RetryLoops        int `json:"retry_loops"`
}

// Anomalies returns the number of anomalies, low-confidence fallbacks included.
func (c Counts) Anomalies() int {
	return c.AuthExpired + c.SilentFallbacks + c.PossibleFallbacks + c.RetryLoops
}

// Row is the Counts of one session, MCP server or tool.
type Row struct {
	Key string `json:"key"`
	// Project is the directory the session belongs to (sessions only).
	Project  string `json:"project,omitempty"`
	Provider string `json:"provider,omitempty"`
	// LastCall is the time of the session's last call in the window (sessions only).
	LastCall time.Time `json:"last_call,omitempty"`
	Counts
}

// Report is the aggregate over all sessions with calls in the window.
type Report struct {
	Window     Window `json:"window"`
	Sessions   int    `json:"sessions"`
	Total      Counts `json:"total"`
	MCPServers []Row  `json:"mcp_servers"`
	Tools      []Row  `json:"tools"`
	BySession  []Row  `json:"sessions_detail"`
}

// Builder accumulates analyzed session graphs into a Report.
type Builder struct {
	window   Window
	sessions []Row
	servers  map[string]*Row
	tools    map[string]*Row
	total    Counts
}

// NewBuilder creates a Builder for window.
func NewBuilder(window Window) *Builder {
	return &Builder{window: window, servers: make(map[string]*Row), tools: make(map[string]*Row)}
}

// Add counts the tool calls of a session that fall in the window, and the anomalies the
// detector raised on them in the analyzed graph g. Sessions without such calls are ignored.
func (b *Builder) Add(sessionID, project string, calls []models.CallNode, g *graph.Graph) {
	session := Row{Key: sessionID, Project: project, Provider: g.Provider}
	for i := range calls {
		n := &calls[i]
		if analyzed := g.Nodes[n.ID]; analyzed != nil {
			n = analyzed
		}
		if n.IsInferredPlaceholder() || !b.window.Contains(n.Timestamp) {
			continue
		}
		rows := []*Counts{&session.Counts, &b.total, &b.row(b.tools, n.Name).Counts}
		if n.MCPServer != "" {
			rows = append(rows, &b.row(b.servers, n.MCPServer).Counts)
		}
		for _, c := range rows {
			countCall(c, n)
		}
		if n.Timestamp.After(session.LastCall) {
			session.LastCall = n.Timestamp
		}
	}
	if session.Calls > 0 {
		b.sessions = append(b.sessions, session)
	}
}

// Report returns the aggregate, with rows sorted by anomalies, then failures, then calls.
func (b *Builder) Report() *Report {
	r := &Report{Window: b.window, Sessions: len(b.sessions), Total: b.total}
	r.MCPServers = sortedRows(b.servers)
	r.Tools = sortedRows(b.tools)
	r.BySession = append([]Row(nil), b.sessions...)
	sortRows(r.BySession)
	return r
}

func (b *Builder) row(m map[string]*Row, key string) *Row {
	if m[key] == nil {
		m[key] = &Row{Key: key}
	}
	return m[key]
}

func countCall(c *Counts, n *models.CallNode) {
	c.Calls++
	switch n.Status {
	case models.StatusFailed, models.StatusTimeout:
		c.Failed++
	case models.StatusDenied:
		c.Denied++
	}
	for _, a := range n.Anomalies {
		switch a.Type {
		case models.AnomalyAuthExpired:
			c.AuthExpired++
		case models.AnomalyRetryLoop:
			c.RetryLoops++
		case models.AnomalySilentFallback:
			if a.Confidence == models.ConfidenceLow {
				c.PossibleFallbacks++
			} else {
				c.SilentFallbacks++
			}
		}
	}
}

func sortedRows(m map[string]*Row) []Row {
	rows := make([]Row, 0, len(m))
	for _, r := range m {
		rows = append(rows, *r)
	}
	sortRows(rows)
	return rows
}

func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Counts, rows[j].Counts
		if a.Anomalies() != b.Anomalies() {
			return a.Anomalies() > b.Anomalies()
		}
		if a.Failed != b.Failed {
			return a.Failed > b.Failed
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return rows[i].Key < rows[j].Key
	})
}
