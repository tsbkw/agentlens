package cli

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/server"
	"github.com/tsbkw/agentlens/internal/watcher"
)

//go:embed default_antigravity.yaml
var defaultAntigravityYAML []byte

// App coordinates CLI commands.
type App struct {
	ActiveProvider *providers.LoadedProvider
}

// NewApp initializes the App with the default Antigravity provider.
func NewApp() (*App, error) {
	loaded, err := providers.LoadProviderFromBytes(defaultAntigravityYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to load default provider: %w", err)
	}
	return &App{ActiveProvider: loaded}, nil
}

// Run executes the given command line arguments.
func (a *App) Run(args []string) error {
	if len(args) < 2 {
		a.PrintHelp()
		return nil
	}

	command := args[1]
	switch command {
	case "version", "--version", "-v":
		fmt.Println("agentlens version 0.1.0")
		return nil
	case "help", "--help", "-h":
		a.PrintHelp()
		return nil
	case "list":
		return a.CmdList()
	case "graph":
		if len(args) < 3 {
			return fmt.Errorf("missing session ID. Usage: agentlens graph <session-id> [--trace]")
		}
		if len(args) >= 4 && (args[3] == "--trace" || args[2] == "--trace") {
			targetID := args[2]
			if targetID == "--trace" {
				targetID = args[3]
			}
			return a.CmdTrace(targetID)
		}
		return a.CmdGraph(args[2])
	case "trace":
		if len(args) < 3 {
			return fmt.Errorf("missing session ID. Usage: agentlens trace <session-id>")
		}
		return a.CmdTrace(args[2])
	case "watch":
		targetID := ""
		if len(args) >= 3 {
			targetID = args[2]
		}
		return a.CmdWatch(targetID)
	case "inspect":
		if len(args) < 3 {
			return fmt.Errorf("missing call ID. Usage: agentlens inspect <call-id>")
		}
		return a.CmdInspect(args[2])
	case "ui":
		port := 8000
		srv := server.NewServer(port, a.ActiveProvider)
		return srv.Start()
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

// CmdList lists all discovered sessions.
func (a *App) CmdList() error {
	col := collector.NewCollector(a.ActiveProvider)
	sessions, err := col.DiscoverSessions()
	if err != nil {
		return fmt.Errorf("failed to discover sessions: %w", err)
	}

	RenderSessionList(os.Stdout, sessions)
	return nil
}

func (a *App) resolveSessionOrFile(target string) (*collector.SessionInfo, error) {
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		return &collector.SessionInfo{
			SessionID:  filepath.Base(target),
			FilePath:   target,
			ProviderID: a.ActiveProvider.Definition.Provider.ID,
			ModTime:    fi.ModTime(),
			SizeBytes:  fi.Size(),
		}, nil
	}

	col := collector.NewCollector(a.ActiveProvider)
	sessions, err := col.DiscoverSessions()
	if err != nil {
		return nil, err
	}

	if target == "" {
		if len(sessions) == 0 {
			return nil, fmt.Errorf("no agent sessions found. Run `agentlens list` to view available sessions")
		}
		return &sessions[0], nil
	}

	for _, s := range sessions {
		if s.SessionID == target || strings.HasPrefix(s.SessionID, target) {
			return &s, nil
		}
	}

	return nil, fmt.Errorf("session or file %q not found. Run `agentlens list` to view available sessions", target)
}

// CmdGraph visualizes the caller -> callee call graph for a session.
func (a *App) CmdGraph(targetID string) error {
	matchedSession, err := a.resolveSessionOrFile(targetID)
	if err != nil {
		return err
	}

	col := collector.NewCollector(a.ActiveProvider)
	data, err := col.IngestSessionFile(matchedSession.FilePath)
	if err != nil {
		return fmt.Errorf("failed to ingest session trace: %w", err)
	}

	builder := graph.NewGraphBuilder()
	g := builder.BuildWithTurns(matchedSession.SessionID, a.ActiveProvider.Definition.Provider.ID, data.Turns, data.Nodes)

	// Run anomaly detection
	det := detector.NewDetector(a.ActiveProvider)
	det.Analyze(g)

	RenderCallGraph(os.Stdout, g)
	return nil
}

// CmdTrace visualizes the chronological turn-by-turn execution trace.
func (a *App) CmdTrace(targetID string) error {
	matchedSession, err := a.resolveSessionOrFile(targetID)
	if err != nil {
		return err
	}

	col := collector.NewCollector(a.ActiveProvider)
	data, err := col.IngestSessionFile(matchedSession.FilePath)
	if err != nil {
		return fmt.Errorf("failed to ingest session trace: %w", err)
	}

	builder := graph.NewGraphBuilder()
	g := builder.BuildWithTurns(matchedSession.SessionID, a.ActiveProvider.Definition.Provider.ID, data.Turns, data.Nodes)

	// Run anomaly detection
	det := detector.NewDetector(a.ActiveProvider)
	det.Analyze(g)

	RenderExecutionTrace(os.Stdout, g)
	return nil
}

// CmdInspect displays detailed diagnostic information for a specific node ID.
func (a *App) CmdInspect(callID string) error {
	col := collector.NewCollector(a.ActiveProvider)
	sessions, err := col.DiscoverSessions()
	if err != nil {
		return err
	}

	for _, s := range sessions {
		data, err := col.IngestSessionFile(s.FilePath)
		if err != nil {
			continue
		}

		builder := graph.NewGraphBuilder()
		g := builder.BuildWithTurns(s.SessionID, a.ActiveProvider.Definition.Provider.ID, data.Turns, data.Nodes)

		det := detector.NewDetector(a.ActiveProvider)
		det.Analyze(g)

		for _, node := range g.Nodes {
			if node.ID == callID || strings.HasPrefix(node.ID, callID) {
				RenderNodeInspect(os.Stdout, node)
				return nil
			}
		}
	}

	return fmt.Errorf("call node %q not found in any discovered sessions", callID)
}

// CmdWatch tails an active session's trace file in real time.
func (a *App) CmdWatch(targetID string) error {
	matchedSession, err := a.resolveSessionOrFile(targetID)
	if err != nil {
		return err
	}

	w := watcher.NewWatcher(a.ActiveProvider, matchedSession.FilePath)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return w.Watch(ctx, os.Stdout)
}

func (a *App) PrintHelp() {
	fmt.Print(`AgentLens 🔍 — Generative AI Call Graph Visualizer (v0.1.0)

Usage:
  agentlens <command> [arguments]

Available Commands:
  list                 List recorded AI interaction sessions (Antigravity/Gemini default)
  graph <session-id>   Render Caller ➔ Callee call graph tracing where Skills/MCPs were called
  trace <session-id>   Render turn-by-turn chronological execution trace tree
  watch [session-id]   Live stream tool calls, results & anomaly alerts from active session
  inspect <call-id>    Display detailed input, output, and anomaly diagnostics for a node
  ui                   Launch local Web UI dashboard
  version              Print version of agentlens
  help                 Print this help message

Online Web Viewer:
  https://tsbkw.github.io/agentlens
`)
}
