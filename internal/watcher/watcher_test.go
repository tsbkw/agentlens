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
