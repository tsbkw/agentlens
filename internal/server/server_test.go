package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/report"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	providerPath := filepath.Join("..", "..", "examples", "providers", "antigravity.yaml")
	provider, err := providers.LoadProviderFromFile(providerPath)
	if err != nil {
		t.Fatalf("Failed to load provider: %v", err)
	}
	return NewServer(8000, provider)
}

func TestServerEndpoints(t *testing.T) {
	h := newTestServer(t).Handler()

	req := httptest.NewRequest("GET", "http://localhost:8000/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for /, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "AgentLens") {
		t.Errorf("Expected 'AgentLens' in index.html body")
	}
}

// A page on another site can point its own hostname at 127.0.0.1 (DNS rebinding); the
// browser then sends that hostname in Host, which the server must refuse.
func TestServerRejectsNonLoopbackHost(t *testing.T) {
	h := newTestServer(t).Handler()

	cases := map[string]int{
		"localhost:8000":     http.StatusOK,
		"LOCALHOST":          http.StatusOK,
		"127.0.0.1:8000":     http.StatusOK,
		"[::1]:8000":         http.StatusOK,
		"evil.example:8000":  http.StatusForbidden,
		"192.168.1.20:8000":  http.StatusForbidden,
		"localhost.evil.com": http.StatusForbidden,
		"":                   http.StatusForbidden,
	}
	for host, want := range cases {
		for _, path := range []string{"/", "/api/sessions"} {
			req := httptest.NewRequest("GET", path, nil)
			req.Host = host
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code == http.StatusForbidden && want != http.StatusForbidden {
				t.Errorf("Host %q %s: got 403, want allowed", host, path)
			}
			if want == http.StatusForbidden && w.Code != http.StatusForbidden {
				t.Errorf("Host %q %s: got %d, want 403", host, path, w.Code)
			}
		}
	}
}

// newClaudeHome lays out the Claude Code sample session, with its subagent, under a fake
// home directory and returns a server over the built-in providers.
func newClaudeHome(t *testing.T) *Server {
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
	return NewServer(8000, builtins...)
}

func get(t *testing.T, h http.Handler, target string, out interface{}) int {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:8000"+target, nil))
	if w.Code == http.StatusOK && out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: invalid JSON: %v", target, err)
		}
	}
	return w.Code
}

func TestReportEndpoint(t *testing.T) {
	h := newClaudeHome(t).Handler()

	var rep report.Report
	if code := get(t, h, "/api/report?since=2026-10-01&until=2026-10-07&project=backend", &rep); code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", code)
	}
	if rep.Sessions != 1 || len(rep.BySession) != 1 {
		t.Fatalf("Expected the sample session, got %+v", rep)
	}
	row := rep.BySession[0]
	if row.Key != "sample_claude_code_session" || row.Project != "-home-dev-backend-api" || row.AuthExpired != 1 || row.LastCall.IsZero() {
		t.Errorf("Unexpected session row: %+v", row)
	}

	if get(t, h, "/api/report?since=2026-10-01&project=frontend", &rep); rep.Sessions != 0 {
		t.Errorf("Expected the project filter to exclude the session, got %d", rep.Sessions)
	}
	if get(t, h, "/api/report?since=2026-10-08", &rep); rep.Sessions != 0 {
		t.Errorf("Expected no calls after the session, got %d", rep.Sessions)
	}
	if code := get(t, h, "/api/report?since=yesterday", nil); code != http.StatusBadRequest {
		t.Errorf("Expected 400 for an invalid window, got %d", code)
	}
}

func TestSessionFilesEndpoint(t *testing.T) {
	h := newClaudeHome(t).Handler()

	var files []TraceFile
	if code := get(t, h, "/api/sessions/sample_claude/files", &files); code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", code)
	}
	want := []string{
		"/sample_claude_code_session.jsonl",
		"/sample_claude_code_session/subagents/agent-a1b2c3d4e5f60718.jsonl",
		"/sample_claude_code_session/subagents/agent-a1b2c3d4e5f60718.meta.json",
	}
	if len(files) != len(want) {
		t.Fatalf("Expected %d files, got %+v", len(want), files)
	}
	for i, f := range files {
		if f.Path != want[i] || f.Text == "" {
			t.Errorf("File %d: expected %s with content, got %q (%d bytes)", i, want[i], f.Path, len(f.Text))
		}
	}

	for _, target := range []string{"/api/sessions/nope/files", "/api/sessions/sample_claude/other", "/api/sessions/sample_claude/files/x"} {
		if code := get(t, h, target, nil); code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", target, code)
		}
	}
}
