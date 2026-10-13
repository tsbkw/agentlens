package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/report"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// toolDefinition is one entry of tools/list.
type toolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

func prop(typ, desc string) map[string]interface{} {
	return map[string]interface{}{"type": typ, "description": desc}
}

func schema(required []string, props map[string]interface{}) map[string]interface{} {
	s := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var windowProps = map[string]interface{}{
	"since":   prop("string", "Start of the period: YYYY-MM-DD, RFC 3339, or <N>d for N days ago."),
	"until":   prop("string", "End of the period (that day included): YYYY-MM-DD or RFC 3339. Open when omitted."),
	"project": prop("string", "Only sessions whose project directory name contains this text."),
}

func withWindow(extra map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range windowProps {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name: "report",
			Description: "Overview of the AI agent sessions recorded on this machine over a period (default: last 7 days): " +
				"totals, calls and anomalies per MCP server, per tool and per session, and the dormant Skills and MCP servers " +
				"(not working as of their last use) with the reason: server fails to connect, waiting for sign-in, auth failed " +
				"and not fixed, denied, replaced by a substitute, failing. Also names tools offered but never called.",
			InputSchema: schema(nil, withWindow(map[string]interface{}{
				"top": prop("integer", "Rows to keep per table (default 15)."),
			})),
		},
		{
			Name: "why",
			Description: "Why a Skill or MCP server is not being used (default period: last 30 days): its outcome in every " +
				"session (offered, failed to connect, waiting for sign-in, calls ok/failed/denied, fallbacks), the other tools " +
				"that reached the same public service instead, a diagnosis with evidence and a recommendation, and, when there " +
				"is something to investigate, a checklist of what to inspect on this machine. Use the report's dormant tools or " +
				"the name in mcp__<server>__ tool names, or a Skill's name.",
			InputSchema: schema([]string{"tool"}, withWindow(map[string]interface{}{
				"tool":         prop("string", "Skill name or MCP server name (case-insensitive; punctuation is folded)."),
				"max_sessions": prop("integer", "Most recent sessions to include (default 40)."),
			})),
		},
		{
			Name:        "list_sessions",
			Description: "Sessions recorded on this machine, newest first: id, agent (provider), project, last modified, size and trace file path.",
			InputSchema: schema(nil, map[string]interface{}{
				"since":   prop("string", "Only sessions modified after this time (YYYY-MM-DD or <N>d; default 7d)."),
				"project": windowProps["project"],
				"limit":   prop("integer", "Maximum rows (default 20)."),
			}),
		},
		{
			Name: "session",
			Description: "Analysis of one session: metrics, anomalies (auth failures, silent fallbacks with confidence, retry " +
				"loops), the caller → callee dependency tree, and the turns. With view=trace, the turn-by-turn list of calls " +
				"(name, status, duration, scope, parent).",
			InputSchema: schema([]string{"session"}, map[string]interface{}{
				"session":   prop("string", "Session id (a unique prefix is enough) or the path of a trace file."),
				"view":      prop("string", "summary (default) or trace."),
				"max_calls": prop("integer", "Calls to include in the trace view (default 200)."),
			}),
		},
		{
			Name:        "inspect_call",
			Description: "One tool call in full: arguments, output, error, duration, status, scope and the anomalies attached to it.",
			InputSchema: schema([]string{"call_id"}, map[string]interface{}{
				"call_id":    prop("string", "Call id (a unique prefix is enough)."),
				"session":    prop("string", "Session id or trace file to search; every session when omitted (slower)."),
				"max_output": prop("integer", "Characters of output to keep (default 4000)."),
			}),
		},
	}
}

// runTool executes one tool by name.
func (s *Server) runTool(ctx context.Context, name string, raw json.RawMessage) (interface{}, error) {
	args := map[string]interface{}{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("arguments must be a JSON object: %v", err)
		}
	}
	switch name {
	case "report":
		return s.toolReport(args)
	case "why":
		return s.toolWhy(args)
	case "list_sessions":
		return s.toolListSessions(args)
	case "session":
		return s.toolSession(args)
	case "inspect_call":
		return s.toolInspect(args)
	}
	return nil, fmt.Errorf("unknown tool %q; available: report, why, list_sessions, session, inspect_call", name)
}

func str(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func num(args map[string]interface{}, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func (s *Server) window(args map[string]interface{}, defaultSince string) (report.Window, error) {
	since := str(args, "since")
	if since == "" {
		since = defaultSince
	}
	return report.ParseWindow(since, str(args, "until"), s.Now())
}

// --- report

type reportResult struct {
	Window       report.Window       `json:"window"`
	Sessions     int                 `json:"sessions"`
	Total        report.Counts       `json:"total"`
	MCPServers   []report.Row        `json:"mcp_servers"`
	Tools        []report.Row        `json:"tools"`
	BySession    []report.Row        `json:"sessions_detail"`
	Dormant      []report.ToolHealth `json:"dormant_tools"`
	Unused       map[string][]string `json:"unused_tools,omitempty"`
	UnusedCounts map[string]int      `json:"unused_counts,omitempty"`
	Note         string              `json:"note,omitempty"`
}

func (s *Server) toolReport(args map[string]interface{}) (interface{}, error) {
	window, err := s.window(args, report.DefaultSince)
	if err != nil {
		return nil, err
	}
	top := num(args, "top", 15)
	rep, err := report.Collect(s.Providers, window, str(args, "project"), func(path string, err error) { s.logf("skipping %s: %v", path, err) })
	if err != nil {
		return nil, err
	}
	cap := func(rows []report.Row) []report.Row {
		if len(rows) > top {
			return rows[:top]
		}
		return rows
	}
	out := reportResult{Window: rep.Window, Sessions: rep.Sessions, Total: rep.Total,
		MCPServers: cap(rep.MCPServers), Tools: cap(rep.Tools), BySession: cap(rep.BySession), Dormant: rep.Dormant()}
	unused := rep.Unused()
	if len(unused) > 0 {
		out.Unused = map[string][]string{}
		out.UnusedCounts = map[string]int{}
		for _, h := range unused {
			out.UnusedCounts[h.Kind]++
			if len(out.Unused[h.Kind]) < 30 {
				out.Unused[h.Kind] = append(out.Unused[h.Kind], h.Key)
			}
		}
	}
	out.Note = fmt.Sprintf("Tables keep the top %d rows. Call `why` with a dormant tool's key for its history and the checks to run.", top)
	return out, nil
}

// --- why

func (s *Server) toolWhy(args map[string]interface{}) (interface{}, error) {
	tool := str(args, "tool")
	if tool == "" {
		return nil, fmt.Errorf("`tool` is required: a Skill name or an MCP server name")
	}
	window, err := s.window(args, "30d")
	if err != nil {
		return nil, err
	}
	ex, err := report.Explain(s.Providers, window, str(args, "project"), tool, func(path string, err error) { s.logf("skipping %s: %v", path, err) })
	if err != nil {
		return nil, err
	}
	// The assistant is the investigator: it gets the structured checks, not a prompt for itself
	ex.Prompt = ""
	if max := num(args, "max_sessions", 40); len(ex.Sessions) > max {
		omitted := len(ex.Sessions) - max
		ex.Sessions = ex.Sessions[omitted:]
		return map[string]interface{}{"explanation": ex, "sessions_omitted": omitted}, nil
	}
	return map[string]interface{}{"explanation": ex}, nil
}

// --- sessions

type sessionRow struct {
	SessionID string    `json:"session_id"`
	Provider  string    `json:"provider"`
	Project   string    `json:"project"`
	Modified  time.Time `json:"modified"`
	SizeBytes int64     `json:"size_bytes"`
	Path      string    `json:"path"`
}

func (s *Server) toolListSessions(args map[string]interface{}) (interface{}, error) {
	window, err := s.window(args, report.DefaultSince)
	if err != nil {
		return nil, err
	}
	sessions, err := collector.DiscoverAllSessions(s.Providers)
	if err != nil {
		return nil, err
	}
	project := str(args, "project")
	limit := num(args, "limit", 20)
	rows := []sessionRow{}
	for _, info := range sessions {
		name := report.ProjectOf(info)
		if info.ModTime.Before(window.Since) || (project != "" && !strings.Contains(name, project)) {
			continue
		}
		rows = append(rows, sessionRow{SessionID: info.SessionID, Provider: info.ProviderID, Project: name, Modified: info.ModTime, SizeBytes: info.SizeBytes, Path: info.FilePath})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Modified.After(rows[j].Modified) })
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return map[string]interface{}{"sessions": rows, "total": total}, nil
}

// --- session

type callRow struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	MCPServer  string   `json:"mcp_server,omitempty"`
	Status     string   `json:"status"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Caller     string   `json:"caller,omitempty"`
	ParentID   string   `json:"parent_id,omitempty"`
	Auxiliary  bool     `json:"auxiliary,omitempty"`
	Error      string   `json:"error,omitempty"`
	Anomalies  []string `json:"anomalies,omitempty"`
}

func (s *Server) toolSession(args map[string]interface{}) (interface{}, error) {
	target := str(args, "session")
	if target == "" {
		return nil, fmt.Errorf("`session` is required: a session id (prefix) or a trace file path")
	}
	info, provider, err := s.resolveSession(target)
	if err != nil {
		return nil, err
	}
	data, g, found, err := analyze(info, provider)
	if err != nil {
		return nil, err
	}
	base := map[string]interface{}{
		"session_id": info.SessionID, "provider": provider.Definition.Provider.ID, "project": report.ProjectOf(*info),
		"path": info.FilePath, "metrics": g.ComputeMetrics(), "anomalies": found.Anomalies, "skipped_lines": data.SkippedLines,
	}
	if data.Availability != nil {
		base["availability"] = data.Availability
	}
	if str(args, "view") == "trace" {
		max := num(args, "max_calls", 200)
		turns := []map[string]interface{}{}
		kept := 0
		for _, t := range data.Turns {
			calls := []callRow{}
			for i := range t.Nodes {
				if kept >= max {
					break
				}
				n := &t.Nodes[i]
				if analyzed := g.Nodes[n.ID]; analyzed != nil {
					n = analyzed
				}
				calls = append(calls, toCallRow(n))
				kept++
			}
			turns = append(turns, map[string]interface{}{"index": t.Index, "prompt": t.Prompt, "timestamp": t.Timestamp, "calls": calls, "call_count": len(t.Nodes)})
		}
		base["turns"] = turns
		if kept >= max {
			base["note"] = fmt.Sprintf("Trace cut at %d calls (max_calls).", max)
		}
		return base, nil
	}
	turns := []map[string]interface{}{}
	for _, t := range data.Turns {
		turns = append(turns, map[string]interface{}{"index": t.Index, "prompt": t.Prompt, "timestamp": t.Timestamp, "call_count": len(t.Nodes)})
	}
	base["turns"] = turns
	base["dependencies"] = g.Dependencies
	return base, nil
}

func toCallRow(n *models.CallNode) callRow {
	row := callRow{ID: n.ID, Name: n.Name, Type: string(n.Type), MCPServer: n.MCPServer, Status: string(n.Status), DurationMs: n.DurationMs,
		Scope: n.ScopeName, Caller: n.CallerScope, ParentID: n.ParentID, Auxiliary: n.Auxiliary, Error: textutil.Truncate(firstLine(n.ErrorMessage), 200, "…")}
	for _, a := range n.Anomalies {
		row.Anomalies = append(row.Anomalies, string(a.Type))
	}
	return row
}

// --- inspect

func (s *Server) toolInspect(args map[string]interface{}) (interface{}, error) {
	callID := str(args, "call_id")
	if callID == "" {
		return nil, fmt.Errorf("`call_id` is required")
	}
	maxOut := num(args, "max_output", 4000)
	var targets []collector.SessionInfo
	byID := map[string]*providers.LoadedProvider{}
	if target := str(args, "session"); target != "" {
		info, provider, err := s.resolveSession(target)
		if err != nil {
			return nil, err
		}
		targets = []collector.SessionInfo{*info}
		byID[info.ProviderID] = provider
	} else {
		all, err := collector.DiscoverAllSessions(s.Providers)
		if err != nil {
			return nil, err
		}
		targets = all
		for _, p := range s.Providers {
			byID[p.Definition.Provider.ID] = p
		}
	}
	for i := range targets {
		info := &targets[i]
		provider := byID[info.ProviderID]
		if provider == nil {
			continue
		}
		_, g, _, err := analyze(info, provider)
		if err != nil {
			continue
		}
		var prefixed []*models.CallNode
		for _, node := range g.OrderedNodes() {
			if node.ID == callID {
				return inspectResult(info, node, maxOut), nil
			}
			if strings.HasPrefix(node.ID, callID) && !node.Synthetic {
				prefixed = append(prefixed, node)
			}
		}
		if len(prefixed) == 1 {
			return inspectResult(info, prefixed[0], maxOut), nil
		}
		if len(prefixed) > 1 {
			return nil, fmt.Errorf("call id %q is ambiguous in session %s: it matches %d calls", callID, info.SessionID, len(prefixed))
		}
	}
	return nil, fmt.Errorf("call %q not found; pass `session` to search one session, or use `session` with view=trace to list call ids", callID)
}

func inspectResult(info *collector.SessionInfo, node *models.CallNode, maxOut int) map[string]interface{} {
	output := providers.TextContent(node.Output)
	truncated := false
	if len(output) > maxOut {
		output = output[:maxOut]
		truncated = true
	}
	return map[string]interface{}{
		"session_id": info.SessionID, "call": node, "output_text": output, "output_truncated": truncated,
	}
}

// --- helpers

// resolveSession finds a session by id prefix, or treats target as a trace file.
func (s *Server) resolveSession(target string) (*collector.SessionInfo, *providers.LoadedProvider, error) {
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		provider := collector.DetectProvider(target, s.Providers)
		if provider == nil {
			return nil, nil, fmt.Errorf("could not detect the trace format of %q", target)
		}
		return &collector.SessionInfo{
			SessionID: strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)), FilePath: target,
			ProviderID: provider.Definition.Provider.ID, ModTime: fi.ModTime(), SizeBytes: fi.Size(),
		}, provider, nil
	}
	sessions, err := collector.DiscoverAllSessions(s.Providers)
	if err != nil {
		return nil, nil, err
	}
	info, err := collector.FindSession(sessions, target)
	if err != nil {
		return nil, nil, err
	}
	if info == nil {
		return nil, nil, fmt.Errorf("session %q not found; call list_sessions", target)
	}
	for _, p := range s.Providers {
		if p.Definition.Provider.ID == info.ProviderID {
			return info, p, nil
		}
	}
	return nil, nil, fmt.Errorf("no provider %q for session %s", info.ProviderID, info.SessionID)
}

// analyze ingests a session and builds its analyzed graph.
func analyze(info *collector.SessionInfo, provider *providers.LoadedProvider) (*collector.SessionData, *graph.Graph, *detector.DetectionResult, error) {
	data, err := collector.NewCollector(provider).IngestSessionFile(info.FilePath)
	if err != nil {
		return nil, nil, nil, err
	}
	g := graph.NewGraphBuilder().BuildWithTurns(info.SessionID, provider.Definition.Provider.ID, data.Turns, data.Nodes)
	found := detector.NewDetector(provider).Analyze(g)
	return data, g, found, nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
