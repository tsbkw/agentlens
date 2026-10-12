package collector

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/providers"
)

func claudeProvider(t *testing.T) *providers.LoadedProvider {
	t.Helper()
	loaded, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestFeedJSONLSkipsBadLinesAndStripsBOM(t *testing.T) {
	call := func(id string) string {
		return `{"type":"assistant","timestamp":"2026-10-07T14:00:00Z","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{}}]}}`
	}
	huge := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"big","name":"Bash","input":{"command":"` + strings.Repeat("x", MaxLineBytes) + `"}}]}}`
	input := "\xEF\xBB\xBF" + `{"type":"user","timestamp":"2026-10-07T13:59:00Z","message":{"content":"first prompt"}}` + "\n" +
		call("a") + "\r\n" +
		"not json at all\n" +
		huge + "\n" +
		"[1,2,3]\n" +
		call("b") // no trailing newline

	data, err := NewCollector(claudeProvider(t)).IngestSessionReader(strings.NewReader(input), "s.jsonl")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	if len(data.Turns) != 1 || data.Turns[0].Prompt != "first prompt" {
		t.Errorf("The first line (after the BOM) must be read as a turn, got %+v", data.Turns)
	}
	if len(data.Nodes) != 2 || data.Nodes[0].ID != "a" || data.Nodes[1].ID != "b" {
		t.Errorf("Expected calls a and b, got %+v", data.Nodes)
	}
	if data.SkippedLines != 3 {
		t.Errorf("Expected 3 skipped lines (text, over-long, array), got %d", data.SkippedLines)
	}
}

func TestDuplicateCallIDsKeepBothCalls(t *testing.T) {
	line := `{"type":"assistant","timestamp":"2026-10-07T14:00:00Z","message":{"content":[{"type":"tool_use","id":"dup","name":"Bash","input":{}}]}}`
	data, err := NewCollector(claudeProvider(t)).IngestSessionReader(strings.NewReader(line+"\n"+line+"\n"+line), "s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, n := range data.Nodes {
		ids = append(ids, n.ID)
	}
	if strings.Join(ids, ",") != "dup,dup#2,dup#3" {
		t.Errorf("Expected suffixed duplicates, got %v", ids)
	}
}

func TestCleanPromptSkipsInjectedTagBlocks(t *testing.T) {
	cases := map[string]string{
		"<system-reminder>\nAs you answer, remember X.\n</system-reminder>\nFix the failing build":   "Fix the failing build",
		"Fix the failing build\n<system-reminder>\nmore</system-reminder>":                           "Fix the failing build",
		"<pasted_content id=\"ab12\">\nsome pasted log\n</pasted_content>\nWhat does this log mean?": "What does this log mean?",
		"<command-name>/review</command-name>\n<command-message>review</command-message>":            "/review",
		"<system-reminder>\nonly a reminder\n</system-reminder>":                                     "[system-reminder] only a reminder",
		"<bash-input>gh pr view 12</bash-input>\n<bash-stdout>#12 open</bash-stdout>":                "[bash-input] gh pr view 12",
		"plain prompt": "plain prompt",
	}
	for in, want := range cases {
		if got := cleanPrompt(in, "command-name"); got != want {
			t.Errorf("cleanPrompt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindSessionPrefersExactAndRejectsAmbiguousPrefix(t *testing.T) {
	sessions := []SessionInfo{
		{SessionID: "abc123", ProviderID: "claude-code"},
		{SessionID: "abc999", ProviderID: "cursor"},
		{SessionID: "abc", ProviderID: "generic-jsonl"},
	}
	if s, err := FindSession(sessions, "abc"); err != nil || s == nil || s.ProviderID != "generic-jsonl" {
		t.Errorf("Exact match must win: %+v, %v", s, err)
	}
	if s, err := FindSession(sessions, "abc1"); err != nil || s == nil || s.SessionID != "abc123" {
		t.Errorf("A unique prefix must match: %+v, %v", s, err)
	}
	if _, err := FindSession(sessions, "abc9"); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if _, err := FindSession(sessions, "ab"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("A shared prefix must be rejected, got %v", err)
	}
	if s, err := FindSession(sessions, "zzz"); err != nil || s != nil {
		t.Errorf("No match must return nil, got %+v, %v", s, err)
	}
}

// A slash command writes its output as a second message in the user role, `!` shell
// commands write their stdout the same way, and an interrupted response leaves a marker.
// None of them is a prompt (turns.skip_regex): the calls that follow stay in the turn of
// the command the user actually issued.
func TestOutputOnlyMessagesAndInterruptionsAreNotTurns(t *testing.T) {
	user := func(content, ts string) string {
		return `{"type":"user","timestamp":"` + ts + `","message":{"content":` + content + `}}`
	}
	call := func(id, ts string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"true"}}]}}`
	}
	input := strings.Join([]string{
		user(`"<command-message>review</command-message>\n<command-name>/review</command-name>\n<command-args>42</command-args>"`, "2026-10-07T10:00:00Z"),
		user(`"<local-command-stdout>Loaded 3 files</local-command-stdout>"`, "2026-10-07T10:00:00.200Z"),
		call("c1", "2026-10-07T10:00:01Z"),
		user(`"<bash-input>ls</bash-input>"`, "2026-10-07T10:01:00Z"),
		user(`"<bash-stdout>README.md</bash-stdout><bash-stderr></bash-stderr>"`, "2026-10-07T10:01:00.300Z"),
		call("c2", "2026-10-07T10:01:01Z"),
		user(`"<local-command-stdout></local-command-stdout>"`, "2026-10-07T10:01:30Z"),
		user(`[{"type":"text","text":"[Request interrupted by user]"}]`, "2026-10-07T10:02:00Z"),
		user(`"continue"`, "2026-10-07T10:02:30Z"),
		call("c3", "2026-10-07T10:02:31Z"),
	}, "\n")

	data, err := NewCollector(claudeProvider(t)).IngestSessionReader(strings.NewReader(input), "s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, turn := range data.Turns {
		var ids []string
		for _, n := range turn.Nodes {
			ids = append(ids, n.ID)
		}
		got = append(got, turn.Prompt+":"+strings.Join(ids, ","))
	}
	want := []string{"/review:c1", "[bash-input] ls:c2", "continue:c3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Turns = %v, want %v", got, want)
	}
}

// The harness announces what the agent can call: skill listings, tool deltas as MCP servers
// connect, servers that failed to connect and servers waiting for a sign-in
// (extraction.availability). They are collected per session, deduplicated, with the MCP
// servers derived from the tool names.
func TestAvailabilityEventsAreCollected(t *testing.T) {
	lines := []string{
		`{"type":"attachment","timestamp":"2026-10-07T10:00:00Z","attachment":{"type":"skill_listing","names":["pr-review","plugin:deploy"]}}`,
		`{"type":"user","timestamp":"2026-10-07T10:00:01Z","message":{"content":"go"}}`,
		`{"type":"attachment","timestamp":"2026-10-07T10:00:02Z","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__github__get_issue","mcp__github__list_prs","mcp__slack__search","ListMcpResourcesTool"],"failedMcpServers":[{"name":"jira","error":"HTTP 401"}],"pendingMcpServers":["datadog"]}}`,
		`{"type":"attachment","timestamp":"2026-10-07T10:05:00Z","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__github__get_issue","mcp__datadog__get_logs"],"failedMcpServers":[{"name":"jira","error":"HTTP 401"},{"name":"wiki","error":"connect ECONNREFUSED"}],"pendingMcpServers":[]}}`,
		`{"type":"attachment","timestamp":"2026-10-07T10:06:00Z","attachment":{"type":"skill_listing","names":["pr-review"]}}`,
	}
	data, err := NewCollector(claudeProvider(t)).IngestSessionReader(strings.NewReader(strings.Join(lines, "\n")), "s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	av := data.Availability
	if av == nil {
		t.Fatal("Expected availability to be collected")
	}
	if got := strings.Join(av.Skills, ","); got != "pr-review,plugin:deploy" {
		t.Errorf("Skills = %q", got)
	}
	if got := strings.Join(av.Tools, ","); got != "mcp__github__get_issue,mcp__github__list_prs,mcp__slack__search,ListMcpResourcesTool,mcp__datadog__get_logs" {
		t.Errorf("Tools = %q", got)
	}
	if got := strings.Join(av.MCPServers, ","); got != "github,slack,datadog" {
		t.Errorf("MCPServers = %q", got)
	}
	if len(av.FailedServers) != 2 || av.FailedServers[0].Name != "jira" || av.FailedServers[0].Occurrences != 2 || av.FailedServers[0].Error != "HTTP 401" ||
		av.FailedServers[1].Name != "wiki" || av.FailedServers[1].Occurrences != 1 {
		t.Errorf("FailedServers = %+v", av.FailedServers)
	}
	if !av.FailedServers[0].Timestamp.Equal(mustTime(t, "2026-10-07T10:00:02Z")) {
		t.Errorf("The first failure keeps the time it was announced, got %v", av.FailedServers[0].Timestamp)
	}
	if got := strings.Join(av.PendingServers, ","); got != "datadog" {
		t.Errorf("PendingServers = %q", got)
	}
	if len(data.Nodes) != 0 || len(data.Turns) != 1 {
		t.Errorf("Availability events must not become calls or turns: %d nodes, %d turns", len(data.Nodes), len(data.Turns))
	}

	// A provider without availability rules collects nothing
	plain, err := NewCollector(claudeProvider(t)).IngestSessionReader(strings.NewReader(lines[1]), "s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Availability != nil {
		t.Errorf("Expected nil availability when nothing was announced, got %+v", plain.Availability)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
