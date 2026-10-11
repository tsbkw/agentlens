package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/providers"
)

//go:embed static/index.html
var indexHTML []byte

// Server provides local HTTP endpoints and embedded UI.
type Server struct {
	Providers []*providers.LoadedProvider
	Port      int
}

// NewServer creates a new local Server serving sessions from the given providers.
func NewServer(port int, provs ...*providers.LoadedProvider) *Server {
	return &Server{
		Providers: provs,
		Port:      port,
	}
}

func (s *Server) providerByID(id string) *providers.LoadedProvider {
	for _, p := range s.Providers {
		if p.Definition.Provider.ID == id {
			return p
		}
	}
	return nil
}

// Start launches the local HTTP server. It listens on the loopback interface only, because
// the API serves full session transcripts.
func (s *Server) Start() error {
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(s.Port))
	fmt.Printf("🚀 AgentLens Web UI is live at: http://localhost:%d\n", s.Port)
	fmt.Println("Press Ctrl+C to stop.")

	return http.ListenAndServe(addr, s.Handler())
}

// Handler returns the HTTP handler serving the UI and the session API. Requests whose Host
// header does not name the loopback interface are rejected, so a page on another site cannot
// read the API through DNS rebinding.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	// API: Sessions list
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions, err := collector.DiscoverAllSessions(s.Providers)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessions)
	})

	// API: Graph for a specific session
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}

		sessionID := parts[0]
		sessions, err := collector.DiscoverAllSessions(s.Providers)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var matched *collector.SessionInfo
		for _, sess := range sessions {
			if sess.SessionID == sessionID || strings.HasPrefix(sess.SessionID, sessionID) {
				matched = &sess
				break
			}
		}

		if matched == nil {
			http.NotFound(w, r)
			return
		}
		provider := s.providerByID(matched.ProviderID)
		if provider == nil {
			http.NotFound(w, r)
			return
		}

		data, err := collector.NewCollector(provider).IngestSessionFile(matched.FilePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		builder := graph.NewGraphBuilder()
		g := builder.BuildWithTurns(matched.SessionID, provider.Definition.Provider.ID, data.Turns, data.Nodes)

		det := detector.NewDetector(provider)
		det.Analyze(g)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g)
	})

	return localOnly(mux)
}

// localOnly rejects requests that were not addressed to localhost or a loopback IP.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
