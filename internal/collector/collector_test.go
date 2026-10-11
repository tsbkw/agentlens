package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

func TestIngestReaderAntigravity(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	loaded, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	col := NewCollector(loaded)

	sampleJSONL := `
{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-07T14:00:00Z","content":"Please check git status"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-07T14:00:01Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"git status"}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-10-07T14:00:02Z","content":"On branch main\nnothing to commit"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-07T14:00:03Z","tool_calls":[{"name":"mcp_github_create_issue","args":{"title":"Deploy Bug"}}]}
{"step_index":4,"source":"MODEL","type":"GENERIC","status":"ERROR","created_at":"2026-10-07T14:00:04Z","content":"401 Unauthorized: token expired"}
`

	reader := strings.NewReader(strings.TrimSpace(sampleJSONL))
	nodes, err := col.IngestReader(reader, "/home/user/.gemini/antigravity-cli/brain/sess-test-42/.system_generated/logs/transcript.jsonl")
	if err != nil {
		t.Fatalf("IngestReader failed: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("Expected 2 CallNodes, got %d", len(nodes))
	}

	// First node: run_command
	if nodes[0].Name != "run_command" {
		t.Errorf("Expected node[0] name 'run_command', got %q", nodes[0].Name)
	}
	if nodes[0].Output != "On branch main\nnothing to commit" {
		t.Errorf("Expected node[0] output to be correlated, got %v", nodes[0].Output)
	}

	// Second node: mcp_github_create_issue (failed)
	if nodes[1].Name != "mcp_github_create_issue" {
		t.Errorf("Expected node[1] name 'mcp_github_create_issue', got %q", nodes[1].Name)
	}
	if nodes[1].Status != models.StatusFailed {
		t.Errorf("Expected node[1] status to be Failed, got %v", nodes[1].Status)
	}
	if nodes[1].Output != "401 Unauthorized: token expired" {
		t.Errorf("Expected node[1] output '401 Unauthorized...', got %v", nodes[1].Output)
	}
}

func TestIngestParallelCallsAntigravity(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	loaded, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	col := NewCollector(loaded)

	// Step 1 calls two tools in parallel; Step 2 and 3 return their results in order
	sampleJSONL := `
{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-07T14:00:00Z","content":"<USER_REQUEST>List files and read readme</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-07T14:00:01Z","tool_calls":[{"name":"list_dir","args":{"Path":"."}},{"name":"view_file","args":{"AbsolutePath":"\"/project/skills/test-skill/SKILL.md\""}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-10-07T14:00:02Z","content":"file1.txt\nfile2.txt"}
{"step_index":3,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-10-07T14:00:03Z","content":"# Test Skill\nSkill documentation"}
`

	reader := strings.NewReader(strings.TrimSpace(sampleJSONL))
	nodes, err := col.IngestReader(reader, "/home/user/.gemini/antigravity-cli/brain/sess-parallel/.system_generated/logs/transcript.jsonl")
	if err != nil {
		t.Fatalf("IngestReader failed: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("Expected 2 CallNodes, got %d", len(nodes))
	}

	if nodes[0].Name != "list_dir" || nodes[0].Output != "file1.txt\nfile2.txt" {
		t.Errorf("Expected list_dir with its own output, got name=%q output=%v", nodes[0].Name, nodes[0].Output)
	}

	if nodes[1].Name != "view_file" || nodes[1].Output != "# Test Skill\nSkill documentation" {
		t.Errorf("Expected view_file with its own output, got name=%q output=%v", nodes[1].Name, nodes[1].Output)
	}

	if nodes[1].ScopeName != "Skill: test-skill" || nodes[1].Type != models.NodeTypeSkill {
		t.Errorf("Expected view_file with quoted path to open Skill: test-skill scope, got type=%v scope=%q", nodes[1].Type, nodes[1].ScopeName)
	}
}

func TestIngestComplexIncidentResponseSample(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	loaded, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	col := NewCollector(loaded)
	samplePath := filepath.Join("..", "..", "examples", "traces", "sample_incident_response.jsonl")

	data, err := col.IngestSessionFile(samplePath)
	if err != nil {
		t.Fatalf("Failed to ingest sample incident response file: %v", err)
	}

	if len(data.Turns) != 3 {
		t.Errorf("Expected 3 turns, got %d", len(data.Turns))
	}
	if len(data.Nodes) != 19 {
		t.Errorf("Expected 19 tool call nodes, got %d", len(data.Nodes))
	}

	// Verify scopes detected
	scopesFound := make(map[string]bool)
	for _, n := range data.Nodes {
		scopesFound[n.CallerScope] = true
	}

	if !scopesFound["Skill: quota-aware-task-runner"] {
		t.Errorf("Expected Skill: quota-aware-task-runner scope to be detected")
	}
	if !scopesFound["Subagent: Database Debugger"] {
		t.Errorf("Expected Subagent: Database Debugger scope to be detected")
	}
	if !scopesFound["Skill: query-optimizer"] {
		t.Errorf("Expected Skill: query-optimizer scope to be detected")
	}
	if !scopesFound["Agent"] {
		t.Errorf("Expected Agent scope to be detected")
	}
}

func TestIngestClaudeCodeSample(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "claude_code.yaml")
	loaded, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}

	col := NewCollector(loaded)
	data, err := col.IngestSessionFile(filepath.Join("..", "..", "examples", "traces", "sample_claude_code_session.jsonl"))
	if err != nil {
		t.Fatalf("Failed to ingest Claude Code sample: %v", err)
	}

	if data.SessionID != "5b0c8f2e-6a4d-4e7b-9c1a-2f3e4d5c6b7a" {
		t.Errorf("Expected sessionId from payload, got %q", data.SessionID)
	}

	// Tool results and meta messages must not start turns
	if len(data.Turns) != 2 {
		t.Fatalf("Expected 2 turns, got %d", len(data.Turns))
	}
	if !strings.HasPrefix(data.Turns[0].Prompt, "Triage the failing deploy") {
		t.Errorf("Unexpected first prompt %q", data.Turns[0].Prompt)
	}
	// Turn 1: Skill, Bash, Agent + 2 calls stitched from the subagent transcript
	if len(data.Turns[0].Nodes) != 5 || len(data.Turns[1].Nodes) != 3 {
		t.Errorf("Expected 5 and 3 nodes per turn, got %d and %d", len(data.Turns[0].Nodes), len(data.Turns[1].Nodes))
	}

	if len(data.Nodes) != 8 {
		t.Fatalf("Expected 8 tool call nodes, got %d", len(data.Nodes))
	}

	byID := make(map[string]models.CallNode)
	for _, n := range data.Nodes {
		byID[n.ID] = n
	}

	gh := byID["toolu_mcp_gh_01"]
	if gh.Status != models.StatusFailed {
		t.Errorf("Expected GitHub MCP call to be failed via is_error, got %v", gh.Status)
	}
	if !strings.Contains(gh.ErrorMessage, "401 Unauthorized") {
		t.Errorf("Expected error message from tool_result, got %q", gh.ErrorMessage)
	}
	if gh.DurationMs != 1200 {
		t.Errorf("Expected duration 1200ms derived from result timestamp, got %d", gh.DurationMs)
	}
	if gh.MCPServer != "github" || gh.Type != models.NodeTypeMCPTool {
		t.Errorf("Expected MCP tool on server github, got %q (%v)", gh.MCPServer, gh.Type)
	}

	// Content-block outputs are flattened to text
	if out, _ := byID["toolu_agent_01"].Output.(string); !strings.HasPrefix(out, "Root cause:") {
		t.Errorf("Expected flattened Agent output, got %v", byID["toolu_agent_01"].Output)
	}

	// Turn snapshots carry correlated results too
	for _, n := range data.Turns[1].Nodes {
		if n.ID == "toolu_mcp_gh_01" && n.Status != models.StatusFailed {
			t.Errorf("Expected turn snapshot to include correlated failure status")
		}
	}
}

func TestIngestClaudeCodeSampleScopes(t *testing.T) {
	loaded, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}
	data, err := NewCollector(loaded).IngestSessionFile(filepath.Join("..", "..", "examples", "traces", "sample_claude_code_session.jsonl"))
	if err != nil {
		t.Fatalf("Failed to ingest Claude Code sample: %v", err)
	}

	callers := make(map[string]string)
	for _, n := range data.Nodes {
		callers[n.ID] = n.CallerScope
	}
	want := map[string]string{
		"toolu_skill_01":     "Agent",
		"toolu_bash_01":      "Skill: incident-triage",
		"toolu_agent_01":     "Skill: incident-triage",
		"toolu_mcp_gh_01":    "Agent", // new turn resets the scope
		"toolu_bash_02":      "Agent",
		"toolu_mcp_slack_01": "Agent",
	}
	for id, scope := range want {
		if callers[id] != scope {
			t.Errorf("%s: expected caller scope %q, got %q", id, scope, callers[id])
		}
	}
}

func TestIngestClaudeCodeSubagentTranscripts(t *testing.T) {
	loaded, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}
	data, err := NewCollector(loaded).IngestSessionFile(filepath.Join("..", "..", "examples", "traces", "sample_claude_code_session.jsonl"))
	if err != nil {
		t.Fatalf("Failed to ingest Claude Code sample: %v", err)
	}

	byID := make(map[string]models.CallNode)
	for _, n := range data.Nodes {
		byID[n.ID] = n
	}

	for _, id := range []string{"toolu_sub_bash_01", "toolu_sub_read_01"} {
		n, ok := byID[id]
		if !ok {
			t.Fatalf("Expected subagent call %s to be stitched into the session", id)
		}
		if n.ParentID != "toolu_agent_01" {
			t.Errorf("%s: expected ParentID toolu_agent_01, got %q", id, n.ParentID)
		}
		if n.CallerScope != "Subagent: Explore" {
			t.Errorf("%s: expected caller scope 'Subagent: Explore', got %q", id, n.CallerScope)
		}
		if n.TurnIndex != byID["toolu_agent_01"].TurnIndex {
			t.Errorf("%s: expected the spawning call's turn %d, got %d", id, byID["toolu_agent_01"].TurnIndex, n.TurnIndex)
		}
		if n.SessionID != data.SessionID {
			t.Errorf("%s: expected session %q, got %q", id, data.SessionID, n.SessionID)
		}
		if n.Output == nil {
			t.Errorf("%s: expected its tool_result to be correlated within the subagent transcript", id)
		}
	}

	// IngestSessionReader has no file system context, so it does not stitch children
	file, err := os.Open(filepath.Join("..", "..", "examples", "traces", "sample_claude_code_session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	nodes, err := NewCollector(loaded).IngestReader(file, "sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 6 {
		t.Errorf("Expected 6 main-thread nodes from a plain reader, got %d", len(nodes))
	}
}

func TestCallsBeforeTheFirstPromptKeepTheirOwnTurn(t *testing.T) {
	loaded, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	asm := NewAssembler(loaded, "s.jsonl")
	feed := func(line string) {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatal(err)
		}
		if _, err := asm.Feed(payload); err != nil {
			t.Fatal(err)
		}
	}
	feed(`{"type":"assistant","timestamp":"2026-10-07T14:00:00Z","message":{"content":[{"type":"tool_use","id":"early","name":"Bash","input":{}}]}}`)
	feed(`{"type":"user","timestamp":"2026-10-07T14:00:05Z","message":{"content":"first prompt"}}`)
	feed(`{"type":"assistant","timestamp":"2026-10-07T14:00:06Z","message":{"content":[{"type":"tool_use","id":"later","name":"Read","input":{}}]}}`)
	data := asm.Finish()

	if len(data.Turns) != 2 || data.Turns[0].Index != 0 || data.Turns[0].Prompt != PreTurnPrompt || data.Turns[1].Index != 1 {
		t.Fatalf("Expected turn 0 for the early call and turn 1 for the prompt, got %+v", data.Turns)
	}
	if len(data.Turns[0].Nodes) != 1 || data.Turns[0].Nodes[0].ID != "early" || len(data.Turns[1].Nodes) != 1 || data.Turns[1].Nodes[0].ID != "later" {
		t.Errorf("Early call must be in turn 0 only: %+v / %+v", data.Turns[0].Nodes, data.Turns[1].Nodes)
	}
	g := graph.NewGraphBuilder().BuildWithTurns("s", "claude-code", data.Turns, data.Nodes)
	if len(g.Parents["early"]) != 1 || g.Parents["early"][0] != "turn-0" {
		t.Errorf("Expected the early call under turn-0 in the graph, got %v", g.Parents["early"])
	}
}

func TestPreviousStepResultIsNotTheNextPrompt(t *testing.T) {
	loaded, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "antigravity.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	input := `{"step_index":0,"type":"USER_INPUT","content":"<USER_REQUEST>first</USER_REQUEST>"}
{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"2026-10-07T12:00:00Z","tool_calls":[{"id":"a","name":"run_command","args":{"CommandLine":"ls"}}]}
{"step_index":2,"type":"USER_INPUT","content":"<USER_REQUEST>second</USER_REQUEST>"}
{"step_index":3,"type":"PLANNER_RESPONSE","created_at":"2026-10-07T12:01:00Z","tool_calls":[{"id":"b","name":"run_command","args":{"CommandLine":"pwd"}}]}
{"step_index":4,"type":"GENERIC","status":"DONE","content":"/home"}
`
	data, err := NewCollector(loaded).IngestSessionReader(strings.NewReader(input), "transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != 2 {
		t.Fatalf("Expected 2 calls, got %d", len(data.Nodes))
	}
	if data.Nodes[0].Output != nil {
		t.Errorf("The next prompt must not become the call's result, got %v", data.Nodes[0].Output)
	}
	if out, _ := data.Nodes[1].Output.(string); out != "/home" {
		t.Errorf("Expected the real result on the second call, got %v", data.Nodes[1].Output)
	}
}
