package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
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
	Provider  *providers.LoadedProvider
	Collector *collector.Collector
	Port      int
}

// NewServer creates a new local Server instance.
func NewServer(port int, provider *providers.LoadedProvider) *Server {
	return &Server{
		Provider:  provider,
		Collector: collector.NewCollector(provider),
		Port:      port,
	}
}

// Start launches the local HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Static UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	// API: Sessions list
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions, err := s.Collector.DiscoverSessions()
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
		sessions, err := s.Collector.DiscoverSessions()
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

		data, err := s.Collector.IngestSessionFile(matched.FilePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		builder := graph.NewGraphBuilder()
		g := builder.BuildWithTurns(matched.SessionID, s.Provider.Definition.Provider.ID, data.Turns, data.Nodes)

		det := detector.NewDetector(s.Provider)
		det.Analyze(g)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g)
	})

	addr := fmt.Sprintf(":%d", s.Port)
	fmt.Printf("🚀 AgentLens Web UI is live at: http://localhost:%d\n", s.Port)
	fmt.Println("Press Ctrl+C to stop.")

	return http.ListenAndServe(addr, mux)
}
