package watcher

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/providers"
)

func TestWatcherLiveStreaming(t *testing.T) {
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load Antigravity provider: %v", err)
	}

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "transcript.jsonl")

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("Failed to create temp log file: %v", err)
	}
	defer f.Close()

	w := NewWatcher(provider, logPath)
	w.PollInterval = 50 * time.Millisecond
	w.FromStart = true

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	// Asynchronously write log lines
	go func() {
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(f, `{"step_index":0,"type":"USER_INPUT","content":"<USER_REQUEST>Test Prompt</USER_REQUEST>"}`+"\n")
		f.Sync()

		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(f, `{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"2026-10-07T12:00:00Z","tool_calls":[{"id":"tc-1","name":"view_file","args":{"AbsolutePath":"/tmp/skills/test-skill/SKILL.md"}}]}`+"\n")
		f.Sync()

		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(f, `{"step_index":2,"type":"GENERIC","status":"DONE","content":"skill content"}`+"\n")
		f.Sync()

		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(f, `{"step_index":3,"type":"PLANNER_RESPONSE","created_at":"2026-10-07T12:00:01Z","tool_calls":[{"id":"tc-2","name":"run_command","args":{"CommandLine":"git status"}}]}`+"\n")
		f.Sync()

		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(f, `{"step_index":4,"type":"GENERIC","status":"DONE","content":"clean"}`+"\n")
		f.Sync()
	}()

	err = w.Watch(ctx, &buf)
	if err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "Test Prompt") {
		t.Errorf("Expected prompt in output, got: %s", outStr)
	}
	if !strings.Contains(outStr, "view_file") {
		t.Errorf("Expected view_file in output, got: %s", outStr)
	}
	if !strings.Contains(outStr, "run_command") {
		t.Errorf("Expected run_command in output, got: %s", outStr)
	}
	if !strings.Contains(outStr, "Skill: test-skill") {
		t.Errorf("Expected active scope Skill: test-skill in output, got: %s", outStr)
	}
}

func TestWatcherClaudeCode(t *testing.T) {
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}

	sample, err := os.ReadFile(filepath.Join("..", "..", "examples", "traces", "sample_claude_code_session.jsonl"))
	if err != nil {
		t.Fatalf("Failed to read sample: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(logPath, sample, 0o600); err != nil {
		t.Fatalf("Failed to write log: %v", err)
	}

	w := NewWatcher(provider, logPath)
	w.PollInterval = 20 * time.Millisecond
	w.FromStart = true

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := w.Watch(ctx, &buf); err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"Triage the failing deploy",
		"[Skill: incident-triage]",
		"[Subagent: Explore]",
		"[MCP:github]",
		"FAILED: mcp__github__create_issue",
		"Authentication Expired",
		"Silent Fallback Detected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Expected %q in watch output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "task-notification") {
		t.Errorf("Task notifications must not be shown as user prompts")
	}
}

func loadClaudeProvider(t *testing.T) *providers.LoadedProvider {
	t.Helper()
	provider, err := providers.LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", "claude_code.yaml"))
	if err != nil {
		t.Fatalf("Failed to load Claude Code provider: %v", err)
	}
	return provider
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("Failed to write %s: %v", dst, err)
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Errorf("Failed to open %s: %v", path, err)
		return
	}
	defer f.Close()
	fmt.Fprint(f, line)
}

func TestWatcherFollowsSubagentTraces(t *testing.T) {
	provider := loadClaudeProvider(t)

	traces := filepath.Join("..", "..", "examples", "traces")
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")
	copyFile(t, filepath.Join(traces, "sample_claude_code_session.jsonl"), logPath)
	for _, name := range []string{"agent-a1b2c3d4e5f60718.jsonl", "agent-a1b2c3d4e5f60718.meta.json"} {
		copyFile(t,
			filepath.Join(traces, "sample_claude_code_session", "subagents", name),
			filepath.Join(dir, "session", "subagents", name))
	}

	w := NewWatcher(provider, logPath)
	w.PollInterval = 20 * time.Millisecond
	w.FromStart = true

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := w.Watch(ctx, &buf); err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	var subagentCalls []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "  ↳ ") && strings.Contains(line, "[Subagent: Explore]") {
			subagentCalls = append(subagentCalls, line)
		}
	}
	if len(subagentCalls) != 2 || !strings.Contains(subagentCalls[0], "Bash") || !strings.Contains(subagentCalls[1], "Read") {
		t.Errorf("Expected the subagent's Bash and Read calls nested under it, got %q in:\n%s", subagentCalls, buf.String())
	}
	if strings.Contains(buf.String(), "USER PROMPT:\033[0m Read the failing deploy-prod") {
		t.Errorf("A subagent's prompt must not be shown as a user turn:\n%s", buf.String())
	}
}

func TestWatcherTailsSubagentSpawnedWhileWatching(t *testing.T) {
	provider := loadClaudeProvider(t)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")
	appendLine(t, logPath, `{"type":"user","message":{"role":"user","content":"old prompt"},"timestamp":"2026-10-07T14:00:00Z"}`+"\n")
	subDir := filepath.Join(dir, "session", "subagents")

	// A subagent already running before watching started: only its new calls are shown
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	appendLine(t, filepath.Join(subDir, "agent-old.meta.json"), `{"toolUseId":"toolu_old"}`)
	appendLine(t, filepath.Join(subDir, "agent-old.jsonl"), `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_old_glob","name":"Glob","input":{}}]},"timestamp":"2026-10-07T14:00:01Z"}`+"\n")

	w := NewWatcher(provider, logPath)
	w.PollInterval = 20 * time.Millisecond

	go func() {
		time.Sleep(80 * time.Millisecond)
		appendLine(t, logPath, `{"type":"user","message":{"role":"user","content":"Investigate"},"timestamp":"2026-10-07T14:01:00Z"}`+"\n")
		appendLine(t, logPath, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_agent","name":"Agent","input":{"subagent_type":"general-purpose","description":"dig"}}]},"timestamp":"2026-10-07T14:01:01Z"}`+"\n")
		time.Sleep(80 * time.Millisecond)

		child := filepath.Join(subDir, "agent-new.jsonl")
		appendLine(t, filepath.Join(subDir, "agent-new.meta.json"), `{"toolUseId":"toolu_agent"}`)
		appendLine(t, child, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_mcp","name":"mcp__github__get_issue","input":{}}]},"timestamp":"2026-10-07T14:01:02Z"}`+"\n")
		// A line flushed in two writes must still be read as one event
		result := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_mcp","content":"401 Unauthorized: token expired","is_error":true}]},"toolUseResult":"Error","timestamp":"2026-10-07T14:01:03Z"}` + "\n"
		appendLine(t, child, result[:40])
		time.Sleep(60 * time.Millisecond)
		appendLine(t, child, result[40:])
		appendLine(t, child, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"gh issue view 1"}}]},"timestamp":"2026-10-07T14:01:04Z"}`+"\n")
		appendLine(t, child, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"ok"}]},"toolUseResult":{"stdout":"ok"},"timestamp":"2026-10-07T14:01:05Z"}`+"\n")

		time.Sleep(60 * time.Millisecond)
		appendLine(t, filepath.Join(subDir, "agent-old.jsonl"), `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_old_grep","name":"Grep","input":{}}]},"timestamp":"2026-10-07T14:01:06Z"}`+"\n")
	}()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	if err := w.Watch(ctx, &buf); err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"Investigate",
		"[Subagent: general-purpose]",
		"↳ ",
		"FAILED: mcp__github__get_issue",
		"Authentication Expired",
		"Silent Fallback Detected: mcp__github__get_issue → Bash",
		"Grep",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Expected %q in watch output, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"old prompt", "Glob"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("Events written before watching started must be skipped, found %q in:\n%s", unwanted, out)
		}
	}
}

func TestWatcherLinksForkedAndUnrecordedSubagents(t *testing.T) {
	provider := loadClaudeProvider(t)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")
	appendLine(t, logPath, "")
	subDir := filepath.Join(dir, "session", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(provider, logPath)
	w.PollInterval = 20 * time.Millisecond
	w.InferAfter = 150 * time.Millisecond

	go func() {
		time.Sleep(60 * time.Millisecond)
		appendLine(t, logPath, `{"type":"user","message":{"content":"Check the page"},"timestamp":"2026-10-07T14:01:00Z"}`+"\n")
		appendLine(t, logPath, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_skill","name":"Skill","input":{"skill":"use-browser"}}]},"timestamp":"2026-10-07T14:01:01Z"}`+"\n")
		// The forked child appears before the Skill's result names it
		appendLine(t, filepath.Join(subDir, "agent-forked.meta.json"), `{"agentType":"general-purpose","name":"use-browser"}`)
		appendLine(t, filepath.Join(subDir, "agent-forked.jsonl"), `{"agentId":"forked","type":"assistant","message":{"content":[{"type":"tool_use","id":"f_nav","name":"WebFetch","input":{}}]},"timestamp":"2026-10-07T14:01:02Z"}`+"\n")
		time.Sleep(60 * time.Millisecond)
		appendLine(t, logPath, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call_skill","content":"launched"}]},"toolUseResult":{"status":"forked","agentId":"forked"},"timestamp":"2026-10-07T14:01:03Z"}`+"\n")

		// A fork that replays the spawning Agent call, and one nothing links to
		appendLine(t, logPath, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call_agent","name":"Agent","input":{"subagent_type":"fork"}}]},"timestamp":"2026-10-07T14:01:04Z"}`+"\n")
		appendLine(t, filepath.Join(subDir, "agent-fork.meta.json"), `{"agentType":"fork","toolUseId":"call_agent"}`)
		appendLine(t, filepath.Join(subDir, "agent-fork.jsonl"),
			`{"agentId":"fork","type":"assistant","message":{"content":[{"type":"tool_use","id":"call_agent","name":"Agent","input":{"subagent_type":"fork"}}]},"timestamp":"2026-10-07T14:01:04Z"}`+"\n"+
				`{"agentId":"fork","type":"assistant","message":{"content":[{"type":"tool_use","id":"k_read","name":"Read","input":{}}]},"timestamp":"2026-10-07T14:01:05Z"}`+"\n")
		appendLine(t, filepath.Join(subDir, "agent-orphan.meta.json"), `{"agentType":"general-purpose","name":"background-check"}`)
		appendLine(t, filepath.Join(subDir, "agent-orphan.jsonl"), `{"agentId":"orphan","type":"assistant","message":{"content":[{"type":"tool_use","id":"o_grep","name":"Grep","input":{}}]},"timestamp":"2026-10-07T14:01:06Z"}`+"\n")
	}()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	if err := w.Watch(ctx, &buf); err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	out := buf.String()
	lines := strings.Split(out, "\n")
	find := func(sub string) string {
		for _, l := range lines {
			if strings.Contains(l, sub) {
				return l
			}
		}
		return ""
	}
	if l := find("WebFetch"); !strings.HasPrefix(l, "  ↳ ") || !strings.Contains(l, "[Skill: use-browser]") || strings.Contains(l, "inferred") {
		t.Errorf("Expected the forked call linked to its Skill call, got %q in:\n%s", l, out)
	}
	if l := find("Read"); !strings.HasPrefix(l, "  ↳ ") || !strings.Contains(l, "[Subagent: fork]") {
		t.Errorf("Expected the fork's own call nested under it, got %q in:\n%s", l, out)
	}
	if strings.Count(out, "➔ \033[1mAgent") != 1 {
		t.Errorf("The spawning call replayed by the fork must be shown once:\n%s", out)
	}
	if !strings.Contains(out, "[Subagent: background-check] spawning call not recorded") {
		t.Errorf("Expected the unrecorded subagent to be announced as inferred:\n%s", out)
	}
	if l := find("Grep"); !strings.Contains(l, "(inferred parent)") {
		t.Errorf("Expected the unrecorded subagent's call marked as inferred, got %q in:\n%s", l, out)
	}
}
