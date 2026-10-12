package report

import (
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
)

func TestBuilderCountsCallsAndAnomaliesInWindow(t *testing.T) {
	day := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	calls := []models.CallNode{
		{ID: "before", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day.Add(-48 * time.Hour), Status: models.StatusFailed},
		{ID: "gh", Name: "mcp__github__get_issue", MCPServer: "github", Type: models.NodeTypeMCPTool, Timestamp: day, Status: models.StatusFailed},
		{ID: "sh", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day.Add(time.Minute), Status: models.StatusSuccess},
		{ID: "no", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day.Add(2 * time.Minute), Status: models.StatusDenied},
		{ID: "inferred-subagent-x", Name: "Subagent", Type: models.NodeTypeSubagent, Timestamp: day, ParentLink: models.ParentLinkInferred},
	}
	g := graph.NewGraphBuilder().Build("s1", "claude-code", calls)
	g.Nodes["gh"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalyAuthExpired}}
	g.Nodes["sh"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalySilentFallback, Confidence: models.ConfidenceHigh}}
	g.Nodes["no"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalySilentFallback, Confidence: models.ConfidenceLow}}

	b := NewBuilder(Window{Since: day.Add(-time.Hour), Until: day.Add(time.Hour)})
	b.Add("s1", "proj-a", calls, g)
	// A session without calls in the window is not counted
	b.Add("s2", "proj-b", calls[:1], graph.NewGraphBuilder().Build("s2", "claude-code", calls[:1]))
	r := b.Report()

	want := Counts{Calls: 3, Failed: 1, Denied: 1, AuthExpired: 1, SilentFallbacks: 1, PossibleFallbacks: 1}
	if r.Sessions != 1 || r.Total != want {
		t.Fatalf("Expected 1 session with %+v, got %d sessions with %+v", want, r.Sessions, r.Total)
	}
	if len(r.MCPServers) != 1 || r.MCPServers[0].Key != "github" || r.MCPServers[0].AuthExpired != 1 {
		t.Errorf("Unexpected MCP server rows %+v", r.MCPServers)
	}
	// Bash has 2 anomalies, the MCP tool 1: sorted by anomalies first
	if len(r.Tools) != 2 || r.Tools[0].Key != "Bash" || r.Tools[0].Calls != 2 {
		t.Errorf("Unexpected tool rows %+v", r.Tools)
	}
	if r.BySession[0].Key != "s1" || r.BySession[0].Project != "proj-a" || r.BySession[0].Provider != "claude-code" {
		t.Errorf("Unexpected session rows %+v", r.BySession)
	}
}

func TestWindowContains(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	w := Window{Since: since, Until: until}
	if !w.Contains(since) || w.Contains(until) || w.Contains(since.Add(-time.Second)) {
		t.Errorf("Window must include Since and exclude Until")
	}
	if !(Window{}).Contains(since) {
		t.Errorf("An empty window contains everything")
	}
}

// Skills and MCP servers are followed across sessions: offered, called, failing to connect,
// waiting for a sign-in. A tool whose last outcome is not a success is dormant with a reason.
func TestToolHealthFollowsSkillsAndServersAcrossSessions(t *testing.T) {
	day := func(d int, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	b := NewBuilder(Window{Since: day(1, 0), Until: day(10, 0)})

	// Session 1: github works, the deploy skill works, jira fails to connect, datadog pending
	s1 := []models.CallNode{
		{ID: "gh1", Name: "mcp__github__get_issue", MCPServer: "github", Type: models.NodeTypeMCPTool, Timestamp: day(2, 9), Status: models.StatusSuccess},
		{ID: "sk1", Name: "Skill", Type: models.NodeTypeSkill, ScopeName: "Skill: deploy", Timestamp: day(2, 10), Status: models.StatusSuccess},
	}
	b.Add("s1", "p", s1, graph.NewGraphBuilder().Build("s1", "claude-code", s1))
	b.AddAvailability("s1", day(2, 9), &models.Availability{
		Skills: []string{"deploy", "release-notes"}, MCPServers: []string{"github", "slack"},
		FailedServers:  []models.ServerFailure{{Name: "jira", Error: "HTTP 401: session is not valid", Timestamp: day(2, 9)}},
		PendingServers: []string{"datadog"},
	})

	// Session 2: github fails on credentials and gh takes over; the deploy skill is denied
	s2 := []models.CallNode{
		{ID: "gh2", Name: "mcp__github__get_issue", MCPServer: "github", Type: models.NodeTypeMCPTool, Timestamp: day(4, 9), Status: models.StatusFailed, ErrorMessage: "Authentication Failed: Bad credentials"},
		{ID: "sh2", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: day(4, 9).Add(time.Minute), Status: models.StatusSuccess},
		{ID: "sk2", Name: "Skill", Type: models.NodeTypeSkill, ScopeName: "Skill: deploy", Timestamp: day(4, 10), Status: models.StatusDenied, ErrorMessage: "The user doesn't want to proceed with this tool use."},
	}
	g2 := graph.NewGraphBuilder().Build("s2", "claude-code", s2)
	g2.Nodes["gh2"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalyAuthExpired}}
	g2.Nodes["sh2"].Anomalies = []models.AnomalyRecord{{Type: models.AnomalySilentFallback, RelatedNodeID: "gh2", Confidence: models.ConfidenceHigh}}
	b.Add("s2", "p", s2, g2)
	b.AddAvailability("s2", day(4, 9), &models.Availability{Skills: []string{"deploy"}, MCPServers: []string{"github"},
		FailedServers: []models.ServerFailure{{Name: "jira", Error: "HTTP 401: session is not valid", Timestamp: day(4, 9)}}})

	// Session 3: nothing is called; github is offered, jira still down
	b.AddAvailability("s3", day(6, 9), &models.Availability{MCPServers: []string{"github"},
		FailedServers: []models.ServerFailure{{Name: "jira", Error: "connect ECONNREFUSED", Timestamp: day(6, 9)}}})

	// A session outside the window is ignored
	b.AddAvailability("old", day(1, 0).Add(-time.Hour), &models.Availability{MCPServers: []string{"github"}})

	r := b.Report()
	by := map[string]ToolHealth{}
	for _, h := range r.ToolHealth {
		by[h.Kind+":"+h.Key] = h
	}

	gh := by["mcp_server:github"]
	if gh.Reason != ReasonAuthUnresolved || gh.Calls != 2 || gh.Failed != 1 || gh.SessionsOffered != 3 || gh.SessionsCalled != 2 ||
		gh.SessionsSinceLastSuccess != 2 || gh.FallbacksFrom != 1 || gh.LastFallbackTo != "Bash" || gh.LastErrorClass != ErrorClassAuth ||
		!gh.LastSuccess.Equal(day(2, 9)) || gh.LastError != "Authentication Failed: Bad credentials" {
		t.Errorf("github: %+v", gh)
	}
	jira := by["mcp_server:jira"]
	if jira.Reason != ReasonConnectionFailing || jira.SessionsFailedConnect != 3 || jira.Calls != 0 || jira.LastConnectError != "connect ECONNREFUSED" || !jira.LastConnectAt.Equal(day(6, 9)) {
		t.Errorf("jira: %+v", jira)
	}
	dd := by["mcp_server:datadog"]
	if dd.Reason != ReasonPendingSignIn || dd.SessionsPending != 1 {
		t.Errorf("datadog: %+v", dd)
	}
	deploy := by["skill:deploy"]
	if deploy.Reason != ReasonDenied || deploy.Calls != 2 || deploy.Denied != 1 || deploy.LastErrorClass != ErrorClassDenied || deploy.SessionsSinceLastSuccess != 2 {
		t.Errorf("deploy: %+v", deploy)
	}
	if notes := by["skill:release-notes"]; notes.Reason != ReasonUnused || notes.SessionsOffered != 1 {
		t.Errorf("release-notes: %+v", notes)
	}
	if slack := by["mcp_server:slack"]; slack.Reason != ReasonUnused {
		t.Errorf("slack: %+v", slack)
	}
	if len(r.Dormant()) != 4 || len(r.Unused()) != 2 {
		t.Errorf("Expected 4 dormant and 2 unused, got %d and %d", len(r.Dormant()), len(r.Unused()))
	}
	// Dormant rows first, then unused ones
	lastDormant, firstUnused := -1, len(r.ToolHealth)
	for i, h := range r.ToolHealth {
		if h.Dormant() {
			lastDormant = i
		} else if h.Reason == ReasonUnused && i < firstUnused {
			firstUnused = i
		}
	}
	if !r.ToolHealth[0].Dormant() || lastDormant > firstUnused {
		t.Errorf("Unexpected order: %v", keys(r.ToolHealth))
	}
}

// A tool whose last call succeeded is healthy even after earlier failures.
func TestToolHealthRecoveredToolIsNotDormant(t *testing.T) {
	day := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	calls := []models.CallNode{
		{ID: "a", Name: "mcp__postgres__query", MCPServer: "postgres", Type: models.NodeTypeMCPTool, Timestamp: day, Status: models.StatusFailed, ErrorMessage: "connection refused"},
		{ID: "b", Name: "mcp__postgres__query", MCPServer: "postgres", Type: models.NodeTypeMCPTool, Timestamp: day.Add(time.Minute), Status: models.StatusSuccess},
	}
	b := NewBuilder(Window{Since: day.Add(-time.Hour)})
	b.Add("s1", "p", calls, graph.NewGraphBuilder().Build("s1", "claude-code", calls))
	r := b.Report()
	if len(r.ToolHealth) != 1 || r.ToolHealth[0].Reason != "" || r.ToolHealth[0].SessionsSinceLastSuccess != 0 || len(r.Dormant()) != 0 {
		t.Errorf("Expected a healthy postgres row, got %+v", r.ToolHealth)
	}
	for text, want := range map[string]string{
		"HTTP 401: Bad credentials":            ErrorClassAuth,
		"MCP server needs you to sign in":      ErrorClassAuth,
		"connect ECONNREFUSED 10.0.0.1:5432":   ErrorClassConnection,
		"Error: Request failed: 503":           ErrorClassConnection,
		"Input validation error: 'q' required": ErrorClassOther,
	} {
		if got := classifyErrorText(text); got != want {
			t.Errorf("classifyErrorText(%q) = %s, want %s", text, got, want)
		}
	}
}

func keys(rows []ToolHealth) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Kind+":"+r.Key+"="+r.Reason)
	}
	return out
}

// The harness names a failed server by its display name while tool names carry a sanitized
// form; both spellings are one tool. A server announced as connected after a failure or a
// sign-in wait is no longer failing or pending.
func TestToolHealthMergesNameSpellingsAndClearsResolvedFailures(t *testing.T) {
	day := func(h int) time.Time { return time.Date(2026, 10, 5, h, 0, 0, 0, time.UTC) }
	b := NewBuilder(Window{Since: day(0)})
	b.AddAvailability("s1", day(9), &models.Availability{
		MCPServers:        []string{"claude_ai_Docs", "datadog"},
		ServerAnnouncedAt: map[string]time.Time{"claude_ai_Docs": day(10), "datadog": day(11)},
		FailedServers:     []models.ServerFailure{{Name: "claude.ai Docs", Error: "No OAuth token found", Timestamp: day(9)}, {Name: "wiki", Error: "timed out", Timestamp: day(9)}},
		PendingServers:    []string{"datadog"},
	})
	r := b.Report()
	by := map[string]ToolHealth{}
	for _, h := range r.ToolHealth {
		by[h.Key] = h
	}
	if len(r.ToolHealth) != 3 {
		t.Fatalf("Expected the two spellings merged into 3 rows, got %v", keys(r.ToolHealth))
	}
	if docs := by["claude_ai_Docs"]; docs.Reason != ReasonUnused || docs.SessionsFailedConnect != 1 {
		t.Errorf("A server connected after its failure is merely unused: %+v", docs)
	}
	if dd := by["datadog"]; dd.Reason != ReasonUnused {
		t.Errorf("A server connected after its sign-in wait is merely unused: %+v", dd)
	}
	if wiki := by["wiki"]; wiki.Reason != ReasonConnectionFailing {
		t.Errorf("wiki: %+v", wiki)
	}
}
