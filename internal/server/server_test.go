package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsbkw/agentlens/internal/providers"
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
