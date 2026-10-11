package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChildTraceParentLinks(t *testing.T) {
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load provider: %v", err)
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		// Turn 1 spawns an Agent subagent and forks a skill; turn 2 has an unrecorded subagent
		"s.jsonl": `
{"type":"user","message":{"content":"first"},"timestamp":"2026-10-07T10:00:00Z"}
{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"call_agent","name":"Agent","input":{"subagent_type":"Explore"}}]},"timestamp":"2026-10-07T10:00:01Z"}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_agent","content":"done"}]},"toolUseResult":{"status":"completed","agentId":"agentA"},"timestamp":"2026-10-07T10:00:09Z"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_skill","name":"Skill","input":{"skill":"web-research"}}]},"timestamp":"2026-10-07T10:00:10Z"}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_skill","content":"Skill launched (forked execution)"}]},"toolUseResult":{"status":"forked","agentId":"agentF"},"timestamp":"2026-10-07T10:00:11Z"}
{"type":"user","message":{"content":"second"},"timestamp":"2026-10-07T11:00:00Z"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_bash","name":"Bash","input":{}}]},"timestamp":"2026-10-07T11:00:01Z"}
`,
		// Fork subagent: replays the spawning message before its own calls
		"s/subagents/agent-agentA.meta.json": `{"agentType":"fork","isFork":true,"toolUseId":"call_agent"}`,
		"s/subagents/agent-agentA.jsonl": `
{"type":"fork-context-ref","agentId":"agentA"}
{"agentId":"agentA","type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"call_agent","name":"Agent","input":{"subagent_type":"Explore"}}]},"timestamp":"2026-10-07T10:00:01Z"}
{"agentId":"agentA","type":"assistant","message":{"content":[{"type":"tool_use","id":"a_read","name":"Read","input":{}}]},"timestamp":"2026-10-07T10:00:02Z"}
`,
		// Forked by a skill: the sidecar names no call, the Skill's result names the agent
		"s/subagents/agent-agentF.meta.json": `{"agentType":"general-purpose","name":"web-research"}`,
		"s/subagents/agent-agentF.jsonl": `
{"agentId":"agentF","type":"assistant","message":{"content":[{"type":"tool_use","id":"f_nav","name":"Bash","input":{}}]},"timestamp":"2026-10-07T10:00:12Z"}
`,
		// Nothing links this one to a call: placed by time, in turn 2
		"s/subagents/agent-agentU.meta.json": `{"agentType":"general-purpose","name":"background-check"}`,
		"s/subagents/agent-agentU.jsonl": `
{"agentId":"agentU","type":"assistant","message":{"content":[{"type":"tool_use","id":"u_grep","name":"Grep","input":{}},{"type":"tool_use","id":"u_agent","name":"Agent","input":{"subagent_type":"reviewer"}}]},"timestamp":"2026-10-07T11:00:05Z"}
`,
		// Spawned from inside the unlinked subagent, linked through its sidecar
		"s/subagents/agent-agentN.meta.json": `{"agentType":"reviewer","toolUseId":"u_agent"}`,
		"s/subagents/agent-agentN.jsonl": `
{"agentId":"agentN","type":"assistant","message":{"content":[{"type":"tool_use","id":"n_glob","name":"Glob","input":{}}]},"timestamp":"2026-10-07T11:00:06Z"}
`,
	})

	data, err := NewCollector(provider).IngestSessionFile(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	byID := make(map[string]models.CallNode)
	for _, n := range data.Nodes {
		if _, dup := byID[n.ID]; dup {
			t.Errorf("Call %s appears twice", n.ID)
		}
		byID[n.ID] = n
	}

	placeholder := "inferred-subagent-agentU"
	cases := []struct {
		id, parent string
		link       models.ParentLink
		turn       int
		scope      string
	}{
		{"call_agent", "", "", 1, "Agent"},
		{"a_read", "call_agent", models.ParentLinkSidecar, 1, "Subagent: Explore"},
		{"f_nav", "call_skill", models.ParentLinkResult, 1, "Skill: web-research"},
		{placeholder, "", models.ParentLinkInferred, 2, "Agent"},
		{"u_grep", placeholder, models.ParentLinkInferred, 2, "Subagent: background-check"},
		{"n_glob", "u_agent", models.ParentLinkSidecar, 2, "Subagent: reviewer"},
	}
	for _, c := range cases {
		n, ok := byID[c.id]
		if !ok {
			t.Errorf("Missing call %s", c.id)
			continue
		}
		if n.ParentID != c.parent || n.ParentLink != c.link || n.TurnIndex != c.turn || n.CallerScope != c.scope {
			t.Errorf("%s: got parent=%q link=%q turn=%d scope=%q, want parent=%q link=%q turn=%d scope=%q",
				c.id, n.ParentID, n.ParentLink, n.TurnIndex, n.CallerScope, c.parent, c.link, c.turn, c.scope)
		}
	}
	if p := byID[placeholder]; !p.IsInferredPlaceholder() || p.ScopeName != "Subagent: background-check" {
		t.Errorf("Unexpected placeholder %+v", p)
	}
	if len(data.Nodes) != 9 {
		t.Errorf("Expected 8 calls plus 1 placeholder, got %d", len(data.Nodes))
	}
}

func ts(s string) time.Time {
	t, _ := providers.ParseTimestamp(s)
	return t
}

func TestTurnAt(t *testing.T) {
	turns := []models.ExecutionTurn{
		{Index: 1, Timestamp: ts("2026-10-07T10:00:00Z")},
		{Index: 2, Timestamp: ts("2026-10-07T11:00:00Z")},
	}
	for at, want := range map[string]int{
		"2026-10-07T09:00:00Z": 1, // before every turn
		"2026-10-07T10:30:00Z": 1,
		"2026-10-07T11:00:00Z": 2,
		"2026-10-07T12:00:00Z": 2,
	} {
		if got := TurnAt(turns, ts(at)); got != want {
			t.Errorf("TurnAt(%s) = %d, want %d", at, got, want)
		}
	}
	if got := TurnAt(nil, ts("2026-10-07T10:00:00Z")); got != 0 {
		t.Errorf("TurnAt without turns = %d, want 0", got)
	}
}
