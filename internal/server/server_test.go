package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestInstanceAndShutdownEndpoints(t *testing.T) {
	srv := newTestServer(t)
	stopped := make(chan struct{})
	srv.shutdown = func() { close(stopped) }
	h := srv.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:8000/api/instance", nil))
	var inst Instance
	if err := json.Unmarshal(w.Body.Bytes(), &inst); err != nil || inst.App != AppName || inst.PID != os.Getpid() {
		t.Fatalf("GET /api/instance = %d %s, want app %q and pid %d", w.Code, w.Body.String(), AppName, os.Getpid())
	}

	// A browser can send a plain POST to localhost; only the custom header gets through.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost:8000/api/shutdown", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("POST /api/shutdown without the header: got %d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:8000/api/shutdown", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/shutdown: got %d, want 405", w.Code)
	}
	select {
	case <-stopped:
		t.Fatal("the server was stopped by a request without the header")
	default:
	}

	req := httptest.NewRequest("POST", "http://localhost:8000/api/shutdown", nil)
	req.Header.Set(shutdownHeader, "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST /api/shutdown with the header: got %d, want 202", w.Code)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the shutdown hook did not run")
	}
}

// freePort reserves a port by listening on it, then releases it for the test to use.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitForInstance polls GET /api/instance on port until a server answers and returns its reply.
func waitForInstance(t *testing.T, port int) Instance {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/instance", port))
		if err == nil {
			var inst Instance
			err = json.NewDecoder(resp.Body).Decode(&inst)
			resp.Body.Close()
			if err == nil {
				return inst
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no agentlens ui answered on port %d", port)
	return Instance{}
}

// A second `agentlens ui` on the same port stops the first one and takes the port over.
func TestStartReplacesARunningInstance(t *testing.T) {
	port := freePort(t)
	first := NewServer(port, newTestServer(t).Providers...)
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Start() }()
	firstInst := waitForInstance(t, port)

	second := NewServer(port, first.Providers...)
	secondDone := make(chan error, 1)
	go func() { secondDone <- second.Start() }()

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("the replaced server returned an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first server did not stop")
	}
	secondInst := waitForInstance(t, port)
	if !secondInst.Started.After(firstInst.Started) {
		t.Fatalf("the port is still served by the first instance (started %v, then %v)", firstInst.Started, secondInst.Started)
	}

	second.shutdown()
	if err := <-secondDone; err != nil {
		t.Fatalf("stopping the second server: %v", err)
	}
}

// A port held by something that is not an agentlens ui is left alone.
func TestStartLeavesOtherProgramsAlone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close() // not HTTP: hang up on every client
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	err = NewServer(port, newTestServer(t).Providers...).Start()
	if err == nil || !strings.Contains(err.Error(), "agentlens ui") || !strings.Contains(err.Error(), "--port") {
		t.Fatalf("Start on a foreign port: got %v, want the listen error with the --port hint", err)
	}
}

func TestWhyEndpoint(t *testing.T) {
	h := newClaudeHome(t).Handler()
	var ex report.Explanation
	if code := get(t, h, "/api/why?tool=github&since=2026-10-01&until=2026-10-07", &ex); code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", code)
	}
	if ex.Tool.Key != "github" || ex.Tool.Kind != report.KindMCPServer || ex.Diagnosis.Reason == "" || ex.Prompt == "" || len(ex.Sessions) != 1 {
		t.Errorf("Unexpected explanation: reason=%q tool=%+v sessions=%d", ex.Diagnosis.Reason, ex.Tool, len(ex.Sessions))
	}
	if code := get(t, h, "/api/why", nil); code != http.StatusBadRequest {
		t.Errorf("Missing tool: expected 400, got %d", code)
	}
	if code := get(t, h, "/api/why?tool=nothing&since=2026-10-01", &ex); code != http.StatusOK || ex.Diagnosis.Reason != report.DiagnosisNotFound {
		t.Errorf("Unknown tool: expected 200 with not_found, got %d %q", code, ex.Diagnosis.Reason)
	}
}
