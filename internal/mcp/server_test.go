package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/providers"
)

// newSampleHome lays the Claude Code sample session out under a fake home directory.
func newSampleHome(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, ".claude", "projects", "-home-dev-backend-api")
	src := filepath.Join("..", "..", "examples", "traces")
	for _, rel := range []string{
		"sample_claude_code_session.jsonl",
		"sample_claude_code_session/subagents/agent-a1b2c3d4e5f60718.jsonl",
		"sample_claude_code_session/subagents/agent-a1b2c3d4e5f60718.meta.json",
	} {
		raw, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builtins, err := providers.LoadBuiltinProviders()
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer("test", builtins...)
	s.Log = io.Discard
	// The sample session is dated 2026-10-07
	s.Now = func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) }
	return s
}

// exchange runs the server over the given request lines and returns one decoded response per
// request that expects one.
func exchange(t *testing.T, s *Server, lines ...string) []map[string]interface{} {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}
	var responses []map[string]interface{}
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 64*1024), MaxLineBytes)
	for scanner.Scan() {
		var r map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, scanner.Text())
		}
		if r["jsonrpc"] != "2.0" {
			t.Errorf("response lacks jsonrpc 2.0: %v", r)
		}
		responses = append(responses, r)
	}
	return responses
}

func call(id int, tool string, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

// toolText returns the text content of a tools/call response and whether it is an error.
func toolText(t *testing.T, r map[string]interface{}) (string, bool) {
	t.Helper()
	result, ok := r["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a result, got %v", r)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func TestInitializeListAndUnknownMethod(t *testing.T) {
	s := newSampleHome(t)
	responses := exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"two","method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
		`[{"jsonrpc":"2.0","id":5,"method":"ping"}]`,
		`not json`,
	)
	if len(responses) != 6 {
		t.Fatalf("expected 6 responses (the notification gets none), got %d: %v", len(responses), responses)
	}
	init := responses[0]["result"].(map[string]interface{})
	if init["protocolVersion"] != "2024-11-05" {
		t.Errorf("an older client keeps its protocol version, got %v", init["protocolVersion"])
	}
	if info := init["serverInfo"].(map[string]interface{}); info["name"] != "agentlens" || info["version"] != "test" {
		t.Errorf("unexpected serverInfo %v", info)
	}
	if responses[1]["id"] != "two" {
		t.Errorf("string ids must be echoed, got %v", responses[1]["id"])
	}
	tools := responses[1]["result"].(map[string]interface{})["tools"].([]interface{})
	names := []string{}
	for _, tool := range tools {
		def := tool.(map[string]interface{})
		names = append(names, def["name"].(string))
		if def["description"] == "" || def["inputSchema"].(map[string]interface{})["type"] != "object" {
			t.Errorf("tool %s needs a description and an object schema", def["name"])
		}
	}
	if strings.Join(names, ",") != "report,why,list_sessions,session,inspect_call" {
		t.Errorf("unexpected tools %v", names)
	}
	if errObj := responses[2]["error"].(map[string]interface{}); errObj["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method must return -32601, got %v", errObj)
	}
	if _, ok := responses[3]["result"]; !ok {
		t.Errorf("ping must return a result, got %v", responses[3])
	}
	for _, r := range responses[4:] {
		if errObj, ok := r["error"].(map[string]interface{}); !ok || errObj["code"].(float64) != codeParseError || r["id"] != nil {
			t.Errorf("a batch or garbage line must get a parse error with a null id, got %v", r)
		}
	}
}

func TestToolsOverTheSampleSession(t *testing.T) {
	s := newSampleHome(t)
	responses := exchange(t, s,
		call(1, "report", `{"since":"2026-10-01","top":5}`),
		call(2, "why", `{"tool":"github","since":"2026-10-01"}`),
		call(3, "list_sessions", `{"since":"2026-10-01"}`),
		call(4, "session", `{"session":"sample_claude"}`),
		call(5, "session", `{"session":"sample_claude","view":"trace","max_calls":3}`),
		call(6, "inspect_call", `{"call_id":"toolu_mcp_gh_01","session":"sample_claude","max_output":20}`),
		call(7, "inspect_call", `{"call_id":"toolu_"}`),
		call(8, "nothing", `{}`),
		call(9, "why", `{}`),
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"report","arguments":{"since":"yesterday"}}}`,
	)
	if len(responses) != 10 {
		t.Fatalf("expected 10 responses, got %d", len(responses))
	}

	text, isErr := toolText(t, responses[0])
	var rep reportResult
	if isErr || json.Unmarshal([]byte(text), &rep) != nil || rep.Sessions != 1 || rep.Total.AuthExpired != 1 || len(rep.Dormant) == 0 || rep.Dormant[0].Key != "github" {
		t.Errorf("report: isError=%v %s", isErr, text)
	}
	if sc := responses[0]["result"].(map[string]interface{})["structuredContent"]; sc == nil {
		t.Errorf("tool results must also carry structuredContent")
	}

	text, isErr = toolText(t, responses[1])
	var why struct {
		Explanation struct {
			Tool          struct{ Key, Kind string }
			Diagnosis     struct{ Reason string }
			Prompt        string
			Investigation *struct {
				Checks []string
			}
		}
	}
	if isErr || json.Unmarshal([]byte(text), &why) != nil || why.Explanation.Tool.Key != "github" || why.Explanation.Diagnosis.Reason == "" || why.Explanation.Prompt != "" ||
		why.Explanation.Investigation == nil || len(why.Explanation.Investigation.Checks) == 0 || !strings.Contains(why.Explanation.Investigation.Checks[0], "claude mcp get github") {
		t.Errorf("why: isError=%v %s", isErr, textutilHead(text))
	}

	text, isErr = toolText(t, responses[2])
	var sessions struct {
		Sessions []sessionRow
		Total    int
	}
	if isErr || json.Unmarshal([]byte(text), &sessions) != nil || sessions.Total != 1 || sessions.Sessions[0].Project != "-home-dev-backend-api" {
		t.Errorf("list_sessions: isError=%v %s", isErr, text)
	}

	text, isErr = toolText(t, responses[3])
	var summary map[string]interface{}
	if isErr || json.Unmarshal([]byte(text), &summary) != nil || summary["session_id"] != "sample_claude_code_session" || summary["dependencies"] == nil || summary["turns"] == nil {
		t.Errorf("session summary: isError=%v %s", isErr, textutilHead(text))
	}
	if anomalies := summary["anomalies"].([]interface{}); len(anomalies) != 2 {
		t.Errorf("expected the sample's 2 anomalies, got %d", len(anomalies))
	}

	text, isErr = toolText(t, responses[4])
	var trace struct {
		Turns []struct {
			Calls     []callRow `json:"calls"`
			CallCount int       `json:"call_count"`
		}
		Note string
	}
	if isErr || json.Unmarshal([]byte(text), &trace) != nil || len(trace.Turns) != 2 || len(trace.Turns[0].Calls) != 3 || trace.Turns[0].CallCount != 5 || !strings.Contains(trace.Note, "max_calls") {
		t.Errorf("session trace: isError=%v %s", isErr, textutilHead(text))
	}

	text, isErr = toolText(t, responses[5])
	var inspected struct {
		OutputText      string `json:"output_text"`
		OutputTruncated bool   `json:"output_truncated"`
		Call            struct{ ID, Status string }
	}
	if isErr || json.Unmarshal([]byte(text), &inspected) != nil || inspected.Call.ID != "toolu_mcp_gh_01" || inspected.Call.Status != "failed" || !inspected.OutputTruncated || len(inspected.OutputText) != 20 {
		t.Errorf("inspect_call: isError=%v %s", isErr, text)
	}

	for i, want := range map[int]string{6: "ambiguous", 7: "unknown tool", 8: "`tool` is required", 9: "invalid since"} {
		text, isErr := toolText(t, responses[i])
		if !isErr || !strings.Contains(text, want) {
			t.Errorf("response %d: expected an error containing %q, got isError=%v %s", i+1, want, isErr, text)
		}
	}
}

func textutilHead(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}
