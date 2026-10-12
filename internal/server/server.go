package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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

	started  time.Time
	shutdown func() // set by Start; stops the running server
}

// Instance identifies a running agentlens ui to another agentlens process (GET /api/instance).
type Instance struct {
	App     string    `json:"app"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// AppName is the value of Instance.App a real agentlens ui reports.
const AppName = "agentlens"

// shutdownHeader must be sent with POST /api/shutdown. A custom header makes the request a
// CORS preflighted one, so a web page cannot stop the server; a program on this machine can.
const shutdownHeader = "X-Agentlens-Shutdown"

// replaceTimeout bounds how long Start waits for a previous instance to release the port.
const replaceTimeout = 5 * time.Second

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
// the API serves full session transcripts. When the port is held by another agentlens ui,
// that instance is asked to stop and this one takes the port over; any other program keeps
// it and Start fails. Start returns nil once this instance is stopped the same way.
func (s *Server) Start() error {
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(s.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		pid, rerr := stopInstance(addr)
		if rerr != nil {
			return fmt.Errorf("cannot listen on %s: %w (%v; try --port)", addr, err, rerr)
		}
		fmt.Printf("↻ Stopped the agentlens ui that was running on port %d (pid %d).\n", s.Port, pid)
		if ln, err = listenUntilFree(addr, replaceTimeout); err != nil {
			return fmt.Errorf("cannot listen on %s: %w (try --port)", addr, err)
		}
	}
	fmt.Printf("🚀 AgentLens Web UI is live at: http://localhost:%d\n", s.Port)
	fmt.Println("Press Ctrl+C to stop.")

	s.started = time.Now()
	httpSrv := &http.Server{Handler: s.Handler()}
	s.shutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
	}
	if err := httpSrv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	fmt.Printf("👋 Stopped: another agentlens ui took over port %d.\n", s.Port)
	return nil
}

// stopInstance asks the agentlens ui listening at addr to stop and returns its PID. It fails
// when whatever listens there is not an agentlens ui.
func stopInstance(addr string) (int, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	base := "http://" + addr
	resp, err := client.Get(base + "/api/instance")
	if err != nil {
		return 0, fmt.Errorf("no agentlens ui answered on that port")
	}
	defer resp.Body.Close()
	var inst Instance
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&inst) != nil || inst.App != AppName {
		return 0, fmt.Errorf("the port is used by a program that is not an agentlens ui, or by one too old to be replaced")
	}

	req, err := http.NewRequest(http.MethodPost, base+"/api/shutdown", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(shutdownHeader, "1")
	resp2, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("the agentlens ui on that port (pid %d) did not answer the stop request: %w", inst.PID, err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		return 0, fmt.Errorf("the agentlens ui on that port (pid %d) refused to stop (HTTP %d)", inst.PID, resp2.StatusCode)
	}
	return inst.PID, nil
}

// listenUntilFree retries net.Listen on addr until it succeeds or timeout passes.
func listenUntilFree(addr string, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the previous agentlens ui did not release the port: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
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

	// API: Identity of this instance, so a newer `agentlens ui` can tell us from another program
	mux.HandleFunc("/api/instance", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, Instance{App: AppName, PID: os.Getpid(), Started: s.started})
	})

	// API: Stop this instance (used by a newer `agentlens ui` taking over the port)
	mux.HandleFunc("/api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get(shutdownHeader) == "" {
			http.Error(w, "missing "+shutdownHeader+" header", http.StatusForbidden)
			return
		}
		if s.shutdown == nil {
			http.Error(w, "this server cannot be stopped remotely", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		go s.shutdown()
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
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "ambiguous") {
				status = http.StatusBadRequest
			}
			http.Error(w, err.Error(), status)
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
	matched, err := collector.FindSession(sessions, id)
	if err != nil || matched == nil {
		return nil, nil, err
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
