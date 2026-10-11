package detector

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

func TestAuthAndSilentFallbackDetection(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	now := time.Now()

	// Scenario: Primary MCP tool fails with 401 auth error,
	// agent immediately executes run_command as fallback.
	nodes := []models.CallNode{
		{
			ID:           "node-mcp-1",
			SessionID:    "test-sess",
			Type:         models.NodeTypeMCPTool,
			Name:         "mcp_github_create_issue",
			MCPServer:    "github",
			Timestamp:    now,
			Status:       models.StatusFailed,
			ErrorMessage: "API call returned 401 Unauthorized: token expired",
		},
		{
			ID:        "node-shell-2",
			SessionID: "test-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(time.Second),
			Status:    models.StatusSuccess,
			Arguments: map[string]interface{}{
				"CommandLine": "gh issue create --title 'Bug'",
			},
		},
	}

	builder := graph.NewGraphBuilder()
	g := builder.Build("test-sess", "antigravity", nodes)

	detector := NewDetector(provider)
	result := detector.Analyze(g)

	if len(result.Anomalies) != 2 {
		t.Fatalf("Expected 2 anomalies (1 auth, 1 fallback), got %d", len(result.Anomalies))
	}

	// Verify Auth Anomaly
	var foundAuth, foundFallback bool
	for _, anom := range result.Anomalies {
		if anom.Type == models.AnomalyAuthExpired {
			foundAuth = true
			if anom.NodeID != "node-mcp-1" {
				t.Errorf("Expected auth anomaly on node-mcp-1, got %s", anom.NodeID)
			}
			if anom.Severity != models.SeverityCritical {
				t.Errorf("Expected Critical severity for auth expiration")
			}
		}
		if anom.Type == models.AnomalySilentFallback {
			foundFallback = true
			if anom.NodeID != "node-shell-2" {
				t.Errorf("Expected fallback anomaly on node-shell-2, got %s", anom.NodeID)
			}
			if anom.RelatedNodeID != "node-mcp-1" {
				t.Errorf("Expected related node to be node-mcp-1, got %s", anom.RelatedNodeID)
			}
		}
	}

	if !foundAuth {
		t.Errorf("Missing expected AnomalyAuthExpired")
	}
	if !foundFallback {
		t.Errorf("Missing expected AnomalySilentFallback")
	}

	// Verify edge was updated to FallbackTo
	if len(g.Edges) > 0 && g.Edges[0].Type != models.EdgeTypeFallbackTo {
		t.Errorf("Expected edge type FallbackTo, got %v", g.Edges[0].Type)
	}
}

func TestRetryLoopDetection(t *testing.T) {
	now := time.Now()

	nodes := []models.CallNode{
		{
			ID:        "call-1",
			SessionID: "loop-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now,
			Status:    models.StatusFailed,
		},
		{
			ID:        "call-2",
			SessionID: "loop-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(time.Second),
			Status:    models.StatusFailed,
		},
		{
			ID:        "call-3",
			SessionID: "loop-sess",
			Type:      models.NodeTypeSystemTool,
			Name:      "run_command",
			Timestamp: now.Add(2 * time.Second),
			Status:    models.StatusFailed,
		},
	}

	builder := graph.NewGraphBuilder()
	g := builder.Build("loop-sess", "generic", nodes)

	detector := NewDetector(nil)
	result := detector.Analyze(g)

	if len(result.Anomalies) != 1 {
		t.Fatalf("Expected 1 loop anomaly, got %d", len(result.Anomalies))
	}

	if result.Anomalies[0].Type != models.AnomalyRetryLoop {
		t.Errorf("Expected AnomalyRetryLoop, got %v", result.Anomalies[0].Type)
	}
}

func TestComplexIncidentResponseAnomalyDetection(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	samplePath := filepath.Join("..", "..", "examples", "traces", "sample_incident_response.jsonl")
	col := collector.NewCollector(provider)
	data, err := col.IngestSessionFile(samplePath)
	if err != nil {
		t.Fatalf("Failed to ingest sample: %v", err)
	}

	builder := graph.NewGraphBuilder()
	g := builder.BuildWithTurns("sample-incident", "antigravity", data.Turns, data.Nodes)

	det := NewDetector(provider)
	result := det.Analyze(g)

	if len(result.Anomalies) == 0 {
		t.Fatalf("Expected anomalies in sample incident response trace, got 0")
	}

	var hasAuth, hasFallback, hasLoop bool
	for _, a := range result.Anomalies {
		switch a.Type {
		case models.AnomalyAuthExpired:
			hasAuth = true
		case models.AnomalySilentFallback:
			hasFallback = true
		case models.AnomalyRetryLoop:
			hasLoop = true
		}
	}

	if !hasAuth {
		t.Errorf("Expected AnomalyAuthExpired in sample trace")
	}
	if !hasFallback {
		t.Errorf("Expected AnomalySilentFallback in sample trace")
	}
	if !hasLoop {
		t.Errorf("Expected AnomalyRetryLoop in sample trace")
	}

	// Verify dependencies
	if len(g.Dependencies) == 0 {
		t.Errorf("Expected dependencies to be computed, got 0")
	}
}

func TestAuthCheckerIgnoresSuccessfulSystemToolOutput(t *testing.T) {
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}
	now := time.Now()

	nodes := []models.CallNode{
		// Reading source code that mentions HTTP 401 is not an auth failure
		{ID: "read", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: now, Status: models.StatusSuccess,
			Output: `if resp.StatusCode == 401 { return ErrUnauthorized }`},
		// Neither is a document fetched by a successful MCP call that mentions 401
		{ID: "mcp", Name: "mcp__notion__fetch", Type: models.NodeTypeMCPTool, Timestamp: now.Add(time.Second), Status: models.StatusSuccess,
			Output: `{"title":"Runbook","text":"If the API returns 401 Unauthorized, rotate the token."}`},
		// A failed system tool is still checked
		{ID: "gh", Name: "Bash", Type: models.NodeTypeSystemTool, Timestamp: now.Add(2 * time.Second), Status: models.StatusFailed,
			Output: "HTTP 401: Bad credentials"},
	}
	g := graph.NewGraphBuilder().Build("s", "claude-code", nodes)
	records := NewAuthChecker(provider).Check(g)

	flagged := make(map[string]bool)
	for _, r := range records {
		flagged[r.NodeID] = true
	}
	if flagged["read"] || flagged["mcp"] {
		t.Errorf("Successful calls must not be flagged as auth failures, got %v", flagged)
	}
	if !flagged["gh"] {
		t.Errorf("Expected the failed Bash call to be flagged, got %v", flagged)
	}
}

func TestDependencyFallbackFlagsRequireConfirmation(t *testing.T) {
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}
	now := time.Now()

	nodes := []models.CallNode{
		// A failed Edit followed by Read is ordinary iteration, not a silent fallback
		{ID: "edit", Name: "Edit", Type: models.NodeTypeSystemTool, CallerScope: "Agent", Timestamp: now, Status: models.StatusFailed},
		{ID: "read", Name: "Read", Type: models.NodeTypeSystemTool, CallerScope: "Agent", Timestamp: now.Add(time.Second), Status: models.StatusSuccess},
		// A failed MCP call followed by Bash is
		{ID: "mcp", Name: "mcp__github__create_issue", Type: models.NodeTypeMCPTool, CallerScope: "Agent", Timestamp: now.Add(2 * time.Second), Status: models.StatusFailed},
		{ID: "bash", Name: "Bash", Type: models.NodeTypeSystemTool, CallerScope: "Agent", Timestamp: now.Add(3 * time.Second), Status: models.StatusSuccess},
	}
	g := graph.NewGraphBuilder().Build("s", "claude-code", nodes)
	NewDetector(provider).Analyze(g)

	for _, dep := range g.Dependencies {
		switch dep.Callee {
		case "Edit":
			if dep.IsFallback {
				t.Errorf("Edit must not be reported as a silent fallback")
			}
		case "mcp__github__create_issue":
			if !dep.IsFallback || dep.FallbackTo != "Bash" {
				t.Errorf("Expected confirmed fallback to Bash, got %v %q", dep.IsFallback, dep.FallbackTo)
			}
		}
	}
}

func TestRetryLoopReportedOncePerStreak(t *testing.T) {
	now := time.Now()
	var nodes []models.CallNode
	add := func(name string, status models.CallStatus) {
		nodes = append(nodes, models.CallNode{
			ID: fmt.Sprintf("n%d", len(nodes)), Name: name, Type: models.NodeTypeMCPTool,
			Timestamp: now.Add(time.Duration(len(nodes)) * time.Second), Status: status,
		})
	}
	for i := 0; i < 6; i++ {
		add("mcp__jira__create_issue", models.StatusFailed)
	}
	add("mcp__jira__create_issue", models.StatusSuccess)
	for i := 0; i < 3; i++ {
		add("mcp__jira__create_issue", models.StatusFailed)
	}

	records := NewLoopDetector().Check(graph.NewGraphBuilder().Build("s", "test", nodes))
	if len(records) != 2 {
		t.Fatalf("Expected one anomaly per streak (2), got %d: %+v", len(records), records)
	}
	if records[0].NodeID != "n2" || !strings.Contains(records[0].Title, "failed 6 times") {
		t.Errorf("First streak: expected anomaly on n2 counting 6 failures, got %+v", records[0])
	}
	if records[1].NodeID != "n9" || !strings.Contains(records[1].Title, "failed 3 times") {
		t.Errorf("Second streak: expected anomaly on n9 counting 3 failures, got %+v", records[1])
	}
}

func TestDeniedAndAuxiliaryCallsInAttemptSequences(t *testing.T) {
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load provider: %v", err)
	}
	transcript := strings.Join([]string{
		`{"type":"user","message":{"content":"go"},"timestamp":"2026-10-07T10:00:00Z"}`,
		// MCP fails, the agent looks up tools, then substitutes the shell: one fallback mcp -> Bash
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"m1","name":"mcp__github__get_issue","input":{}}]},"timestamp":"2026-10-07T10:00:01Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"m1","content":"Authentication Failed: Bad credentials","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:02Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"ts","name":"ToolSearch","input":{"query":"github"}}]},"timestamp":"2026-10-07T10:00:03Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"ts","content":"No such tool available","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:04Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"gh issue view 1"}}]},"timestamp":"2026-10-07T10:00:05Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b1","content":"ok"}]},"toolUseResult":{"stdout":"ok"},"timestamp":"2026-10-07T10:00:06Z"}`,
		// The user rejects an edit; the next call is not a fallback from it
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"e1","name":"mcp__notion__update_page","input":{}}]},"timestamp":"2026-10-07T10:00:07Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"e1","content":"The user doesn't want to proceed with this tool use. The tool use was rejected.","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:08Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"g1","name":"Grep","input":{}}]},"timestamp":"2026-10-07T10:00:09Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"g1","content":"match"}]},"toolUseResult":{"stdout":"match"},"timestamp":"2026-10-07T10:00:10Z"}`,
		// Failures separated only by bookkeeping and a denied call still form one streak
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{}}]},"timestamp":"2026-10-07T10:00:11Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":"File does not exist.","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:12Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"td","name":"TaskUpdate","input":{}}]},"timestamp":"2026-10-07T10:00:13Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r2","name":"Read","input":{}}]},"timestamp":"2026-10-07T10:00:14Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r2","content":"File does not exist.","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:15Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"d1","name":"Bash","input":{"command":"rm -rf build"}}]},"timestamp":"2026-10-07T10:00:16Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"d1","content":"Permission for this action was denied by the Claude Code auto mode classifier.","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:17Z"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r3","name":"Read","input":{}}]},"timestamp":"2026-10-07T10:00:18Z"}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r3","content":"File does not exist.","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T10:00:19Z"}`,
	}, "\n")

	data, err := collector.NewCollector(provider).IngestSessionReader(strings.NewReader(transcript), "s.jsonl")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	status := map[string]models.CallStatus{}
	auxiliary := map[string]bool{}
	for _, n := range data.Nodes {
		status[n.ID], auxiliary[n.ID] = n.Status, n.Auxiliary
	}
	if status["e1"] != models.StatusDenied || status["d1"] != models.StatusDenied || status["m1"] != models.StatusFailed {
		t.Errorf("Expected refusals as denied and tool errors as failed, got %v", status)
	}
	if !auxiliary["ts"] || !auxiliary["td"] || auxiliary["b1"] {
		t.Errorf("Unexpected auxiliary flags %v", auxiliary)
	}

	g := graph.NewGraphBuilder().BuildWithTurns("s", "claude-code", data.Turns, data.Nodes)
	res := NewDetector(provider).Analyze(g)
	var got []string
	for _, a := range res.Anomalies {
		switch a.Type {
		case models.AnomalySilentFallback:
			got = append(got, "fallback:"+a.RelatedNodeID+"->"+a.NodeID)
		case models.AnomalyRetryLoop:
			got = append(got, "loop:"+a.Title)
		}
	}
	want := []string{"fallback:m1->b1", "loop:Retry Loop / Thrashing: Read failed 3 times"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Expected %v, got %v", want, got)
	}
	if m := g.ComputeMetrics(); m.FailedCalls != 5 || m.DeniedCalls != 2 {
		t.Errorf("Expected 5 failed and 2 denied calls, got %+v", m)
	}
}

func TestMatchContextQuotesTheErrorLine(t *testing.T) {
	text := "stdout line\nError: HTTP 401: Bad credentials (https://api.example.com/graphql)\nmore"
	loc := []int{strings.Index(text, "401"), strings.Index(text, "401") + 3}
	if got := matchContext(text, loc, 120); got != "Error: HTTP 401: Bad credentials (https://api.example.com/graphql)" {
		t.Errorf("matchContext = %q", got)
	}

	long := strings.Repeat("x", 300) + " 401 Unauthorized " + strings.Repeat("y", 300)
	i := strings.Index(long, "401")
	got := matchContext(long, []int{i, i + 3}, 60)
	if !strings.Contains(got, "401 Unauthorized") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || len([]rune(got)) > 63 {
		t.Errorf("matchContext must keep the match in a short window, got %q", got)
	}
}

func TestRetryLoopInsideASkillScope(t *testing.T) {
	now := time.Now()
	turns := []models.ExecutionTurn{{Index: 1, Prompt: "go", Timestamp: now}}
	nodes := []models.CallNode{
		{ID: "skill", TurnIndex: 1, Type: models.NodeTypeSkill, Name: "Skill", ScopeName: "Skill: deploy", Timestamp: now, Status: models.StatusSuccess},
	}
	for i := 1; i <= 3; i++ {
		nodes = append(nodes, models.CallNode{ID: fmt.Sprintf("b%d", i), TurnIndex: 1, Type: models.NodeTypeSystemTool, Name: "Bash", CallerScope: "Skill: deploy", Timestamp: now.Add(time.Duration(i) * time.Second), Status: models.StatusFailed})
	}
	g := graph.NewGraphBuilder().BuildWithTurns("s", "p", turns, nodes)
	result := NewDetector(nil).Analyze(g)
	var loops int
	for _, a := range result.Anomalies {
		if a.Type == models.AnomalyRetryLoop {
			loops++
		}
	}
	if loops != 1 {
		t.Errorf("Expected the streak inside the Skill scope to be reported once, got %d in %+v", loops, result.Anomalies)
	}
}
