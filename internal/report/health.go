package report

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// Kinds of the tools whose health is followed across sessions.
const (
	KindMCPServer = "mcp_server"
	KindSkill     = "skill"
)

// Reasons a Skill or MCP server is reported as dormant: it is not working as of its last
// use, or it was offered and never used.
const (
	// ReasonConnectionFailing: the harness could not connect to the server in a session
	// after the tool's last success.
	ReasonConnectionFailing = "connection_failing"
	// ReasonPendingSignIn: the server waited for a sign-in and has had no success since.
	ReasonPendingSignIn = "pending_signin"
	// ReasonAuthUnresolved: the last call failed on credentials and nothing succeeded since.
	ReasonAuthUnresolved = "auth_unresolved"
	// ReasonDenied: the last call was refused by the user or a permission policy.
	ReasonDenied = "denied"
	// ReasonReplaced: the tool failed and a substitute took over (silent fallback), with no
	// success since.
	ReasonReplaced = "replaced"
	// ReasonFailing: the last call failed for another reason and nothing succeeded since.
	ReasonFailing = "failing"
	// ReasonUnused: offered in at least one session, never called in the window.
	ReasonUnused = "unused"
)

// Error classes of a tool's last failure.
const (
	ErrorClassAuth       = "auth"
	ErrorClassConnection = "connection"
	ErrorClassDenied     = "denied"
	ErrorClassOther      = "other"
)

// ToolHealth follows one Skill or MCP server across the sessions of the window.
type ToolHealth struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Counts
	// SessionsOffered counts the sessions in which the harness listed the tool as callable
	// (or that called it).
	SessionsOffered int `json:"sessions_offered"`
	SessionsCalled  int `json:"sessions_called"`
	// SessionsFailedConnect counts the sessions in which the server failed to connect.
	SessionsFailedConnect int `json:"sessions_failed_connect,omitempty"`
	// SessionsPending counts the sessions in which the server waited for a sign-in.
	SessionsPending int `json:"sessions_pending,omitempty"`
	// SessionsSinceLastSuccess counts the sessions of the window that started after the last
	// successful call (every session when there was none).
	SessionsSinceLastSuccess int `json:"sessions_since_last_success"`

	LastCall    time.Time `json:"last_call,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	// LastError is the first line of the last failed call's error.
	LastError      string `json:"last_error,omitempty"`
	LastErrorClass string `json:"last_error_class,omitempty"`
	// LastConnectError is the error of the last connection failure announced by the harness.
	LastConnectError string    `json:"last_connect_error,omitempty"`
	LastConnectAt    time.Time `json:"last_connect_at,omitempty"`
	LastPendingAt    time.Time `json:"last_pending_at,omitempty"`
	// LastOfferedAt is the last time the server's tools were announced (connected).
	LastOfferedAt time.Time `json:"last_offered_at,omitempty"`

	// FallbacksFrom counts the silent fallbacks in which this tool was the failed call.
	FallbacksFrom  int       `json:"fallbacks_from,omitempty"`
	LastFallbackTo string    `json:"last_fallback_to,omitempty"`
	LastFallbackAt time.Time `json:"last_fallback_at,omitempty"`

	// Reason is empty while the tool works; otherwise one of the Reason* values.
	Reason string `json:"reason,omitempty"`
}

// Dormant reports whether the tool is not working as of its last use (Reason set and not
// merely unused).
func (h ToolHealth) Dormant() bool {
	return h.Reason != "" && h.Reason != ReasonUnused
}

// healthBuilder accumulates ToolHealth rows.
type healthBuilder struct {
	tools    map[string]*ToolHealth
	sessions []time.Time // start time of every session seen, in the window
	// per tool, the sessions that offered / called it
	offered map[string]map[string]bool
	called  map[string]map[string]bool
	failed  map[string]map[string]bool
	pending map[string]map[string]bool
}

func newHealthBuilder() *healthBuilder {
	return &healthBuilder{
		tools:   make(map[string]*ToolHealth),
		offered: make(map[string]map[string]bool),
		called:  make(map[string]map[string]bool),
		failed:  make(map[string]map[string]bool),
		pending: make(map[string]map[string]bool),
	}
}

// toolID identifies a tool across the spellings of its name: the harness lists a server by
// its display name ("claude.ai Docs") while tool names carry a sanitized form
// ("mcp__claude_ai_Docs__…"), so keys are compared case-insensitively with every run of
// other characters as one underscore.
func toolID(kind, key string) string {
	return kind + "\x00" + normalizeKey(key)
}

var nonKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeKey(key string) string {
	return strings.Trim(nonKeyChars.ReplaceAllString(strings.ToLower(key), "_"), "_")
}

func (hb *healthBuilder) tool(kind, key string) *ToolHealth {
	id := toolID(kind, key)
	if hb.tools[id] == nil {
		hb.tools[id] = &ToolHealth{Key: key, Kind: kind}
	}
	return hb.tools[id]
}

func mark(set map[string]map[string]bool, tool *ToolHealth, sessionID string) {
	id := toolID(tool.Kind, tool.Key)
	if set[id] == nil {
		set[id] = make(map[string]bool)
	}
	set[id][sessionID] = true
}

// session records a session's start so later rows can count sessions since a success.
func (hb *healthBuilder) session(start time.Time) {
	if !start.IsZero() {
		hb.sessions = append(hb.sessions, start)
	}
}

// call records one call of a Skill or MCP server.
func (hb *healthBuilder) call(sessionID string, n *models.CallNode) {
	var tool *ToolHealth
	switch {
	case n.MCPServer != "":
		tool = hb.tool(KindMCPServer, n.MCPServer)
	case n.Type == models.NodeTypeSkill && strings.HasPrefix(n.ScopeName, "Skill: "):
		tool = hb.tool(KindSkill, strings.TrimPrefix(n.ScopeName, "Skill: "))
	default:
		return
	}
	countCall(&tool.Counts, n)
	mark(hb.offered, tool, sessionID)
	mark(hb.called, tool, sessionID)
	if n.Timestamp.After(tool.LastCall) {
		tool.LastCall = n.Timestamp
	}
	switch n.Status {
	case models.StatusSuccess:
		if n.Timestamp.After(tool.LastSuccess) {
			tool.LastSuccess = n.Timestamp
		}
	case models.StatusFailed, models.StatusTimeout, models.StatusDenied:
		if !n.Timestamp.Before(tool.LastFailure) {
			tool.LastFailure = n.Timestamp
			tool.LastError = firstLine(n.ErrorMessage)
			if tool.LastError == "" {
				tool.LastError = firstLine(textOf(n.Output))
			}
			tool.LastErrorClass = classifyFailure(n)
		}
	}
	for _, a := range n.Anomalies {
		if a.Type == models.AnomalyAuthExpired && n.Timestamp.Equal(tool.LastFailure) {
			tool.LastErrorClass = ErrorClassAuth
		}
	}
}

// fallback records that the failed call `from` was followed by substitute `to`.
func (hb *healthBuilder) fallback(from *models.CallNode, to string, at time.Time) {
	var tool *ToolHealth
	switch {
	case from.MCPServer != "":
		tool = hb.tool(KindMCPServer, from.MCPServer)
	case from.Type == models.NodeTypeSkill && strings.HasPrefix(from.ScopeName, "Skill: "):
		tool = hb.tool(KindSkill, strings.TrimPrefix(from.ScopeName, "Skill: "))
	default:
		return
	}
	tool.FallbacksFrom++
	if !at.Before(tool.LastFallbackAt) {
		tool.LastFallbackAt = at
		tool.LastFallbackTo = to
	}
}

// availability records what a session offered and which servers were down or pending.
func (hb *healthBuilder) availability(sessionID string, at time.Time, av *models.Availability) {
	if av == nil {
		return
	}
	for _, name := range av.Skills {
		mark(hb.offered, hb.tool(KindSkill, name), sessionID)
	}
	for _, name := range av.MCPServers {
		tool := hb.tool(KindMCPServer, name)
		mark(hb.offered, tool, sessionID)
		if announced, ok := av.ServerAnnouncedAt[name]; ok && announced.After(tool.LastOfferedAt) {
			tool.LastOfferedAt = announced
		} else if !ok && at.After(tool.LastOfferedAt) {
			tool.LastOfferedAt = at
		}
	}
	for _, f := range av.FailedServers {
		tool := hb.tool(KindMCPServer, f.Name)
		mark(hb.failed, tool, sessionID)
		when := f.Timestamp
		if when.IsZero() {
			when = at
		}
		if !when.Before(tool.LastConnectAt) {
			tool.LastConnectAt = when
			tool.LastConnectError = firstLine(f.Error)
		}
	}
	for _, name := range av.PendingServers {
		tool := hb.tool(KindMCPServer, name)
		mark(hb.pending, tool, sessionID)
		if at.After(tool.LastPendingAt) {
			tool.LastPendingAt = at
		}
	}
}

// rows finalizes the health rows: session counts, reason, order (dormant first, longest
// silence first).
func (hb *healthBuilder) rows() []ToolHealth {
	rows := make([]ToolHealth, 0, len(hb.tools))
	for id, tool := range hb.tools {
		tool.SessionsOffered = len(hb.offered[id])
		tool.SessionsCalled = len(hb.called[id])
		tool.SessionsFailedConnect = len(hb.failed[id])
		tool.SessionsPending = len(hb.pending[id])
		for _, start := range hb.sessions {
			if tool.LastSuccess.IsZero() || start.After(tool.LastSuccess) {
				tool.SessionsSinceLastSuccess++
			}
		}
		tool.Reason = reasonFor(tool)
		rows = append(rows, *tool)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Dormant() != b.Dormant() {
			return a.Dormant()
		}
		if (a.Reason == ReasonUnused) != (b.Reason == ReasonUnused) {
			return b.Reason == ReasonUnused
		}
		if a.SessionsSinceLastSuccess != b.SessionsSinceLastSuccess {
			return a.SessionsSinceLastSuccess > b.SessionsSinceLastSuccess
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Key < b.Key
	})
	return rows
}

// reasonFor decides whether a tool is dormant and why, from its last outcomes. A connection
// failure or sign-in wait counts only when the server was not announced as connected later.
func reasonFor(t *ToolHealth) string {
	after := func(ts time.Time) bool { return !ts.IsZero() && ts.After(t.LastSuccess) }
	unresolved := func(ts time.Time) bool { return after(ts) && !ts.Before(t.LastOfferedAt) }
	switch {
	case unresolved(t.LastConnectAt):
		return ReasonConnectionFailing
	case unresolved(t.LastPendingAt) && !after(t.LastCall):
		return ReasonPendingSignIn
	case t.Calls == 0:
		if t.SessionsOffered > 0 {
			return ReasonUnused
		}
		return ""
	case !after(t.LastFailure):
		return "" // the last call succeeded
	}
	switch t.LastErrorClass {
	case ErrorClassDenied:
		return ReasonDenied
	case ErrorClassAuth:
		return ReasonAuthUnresolved
	}
	if after(t.LastFallbackAt) {
		return ReasonReplaced
	}
	return ReasonFailing
}

var (
	authErrorRe = regexp.MustCompile(`(?i)\b40[13]\b|unauthori[sz]ed|forbidden|bad credentials|credential|token|sign[ -]?in|log[ -]?in|authenticat|authoriz|oauth|invalid_grant|api[ _-]?key`)
	connErrorRe = regexp.MustCompile(`(?i)connect|timed?[ -]?out|refused|unreachable|enotfound|econn|network|dns|\b50[234]\b|unavailable|not running|spawn|enoent|no such file|rate (?:limit|exceeded)`)
)

// classifyFailure labels a failed or denied call's error.
func classifyFailure(n *models.CallNode) string {
	if n.Status == models.StatusDenied {
		return ErrorClassDenied
	}
	text := n.ErrorMessage
	if text == "" {
		text = textOf(n.Output)
	}
	return classifyErrorText(text)
}

// classifyErrorText labels an error message: credentials, connectivity or other.
func classifyErrorText(text string) string {
	switch {
	case authErrorRe.MatchString(text):
		return ErrorClassAuth
	case connErrorRe.MatchString(text):
		return ErrorClassConnection
	}
	return ErrorClassOther
}

func textOf(v interface{}) string {
	return providers.TextContent(v)
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return textutil.Truncate(line, 160, "…")
		}
	}
	return ""
}
