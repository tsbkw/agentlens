package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/report"
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
		writeJSON(w, sessions)
	})

	// API: Aggregate over the sessions of a period (see `agentlens report`)
	mux.HandleFunc("/api/report", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		window, err := report.ParseWindow(q.Get("since"), q.Get("until"), time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rep, err := report.Collect(s.Providers, window, q.Get("project"), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, rep)
	})

	// API: Graph for a specific session, or with /files its raw trace files
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "files") {
			http.NotFound(w, r)
			return
		}

		matched, provider, err := s.findSession(parts[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if matched == nil {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 2 {
			files, err := traceFiles(provider, matched.FilePath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, files)
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

		writeJSON(w, g)
	})

	return localOnly(mux)
}

// findSession returns the session whose ID is id or starts with it, and its provider. It
// returns a nil session when there is none.
func (s *Server) findSession(id string) (*collector.SessionInfo, *providers.LoadedProvider, error) {
	sessions, err := collector.DiscoverAllSessions(s.Providers)
	if err != nil {
		return nil, nil, err
	}
	var matched *collector.SessionInfo
	for i := range sessions {
		if sessions[i].SessionID == id {
			matched = &sessions[i]
			break
		}
		if matched == nil && strings.HasPrefix(sessions[i].SessionID, id) {
			matched = &sessions[i]
		}
	}
	if matched == nil {
		return nil, nil, nil
	}
	if provider := s.providerByID(matched.ProviderID); provider != nil {
		return matched, provider, nil
	}
	return nil, nil, nil
}

// TraceFile is a trace file of a session, with its path relative to the session's directory.
type TraceFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// traceFiles reads the trace at mainPath and its child traces and sidecars, the same files a
// user would drop into the viewer.
func traceFiles(provider *providers.LoadedProvider, mainPath string) ([]TraceFile, error) {
	paths := []string{mainPath}
	children, err := collector.FindChildTraces(provider, mainPath)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		paths = append(paths, c.Path, c.SidecarPath)
	}

	dir := filepath.Dir(mainPath)
	files := make([]TraceFile, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil, err
		}
		files = append(files, TraceFile{Path: "/" + filepath.ToSlash(rel), Text: string(raw)})
	}
	return files, nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
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
