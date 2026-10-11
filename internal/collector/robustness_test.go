package collector

import (
	"path/filepath"
	"strings"
	"testing"

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
