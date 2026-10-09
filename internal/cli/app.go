package cli

import (
	"context"
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

// App coordinates CLI commands.
type App struct {
	// Builtins are the provider definitions embedded in the binary, default first.
	Builtins []*providers.LoadedProvider
	// Selected is the provider chosen via --provider / AGENTLENS_PROVIDER; nil means auto-detect.
	Selected *providers.LoadedProvider
}

// NewApp initializes the App with the built-in provider definitions.
func NewApp() (*App, error) {
	builtins, err := providers.LoadBuiltinProviders()
	if err != nil {
		return nil, fmt.Errorf("failed to load built-in providers: %w", err)
	}
	return &App{Builtins: builtins}, nil
}

// candidates returns the providers to search: the selected one, or every built-in.
func (a *App) candidates() []*providers.LoadedProvider {
	if a.Selected != nil {
		return []*providers.LoadedProvider{a.Selected}
	}
	return a.Builtins
}

// extractProviderFlag removes --provider/-p from args and returns the remaining args and its value.
func extractProviderFlag(args []string) ([]string, string, error) {
	var rest []string
	spec := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--provider" || arg == "-p":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("%s requires a value (provider ID or YAML path)", arg)
			}
			spec = args[i+1]
			i++
		case strings.HasPrefix(arg, "--provider="):
			spec = strings.TrimPrefix(arg, "--provider=")
		default:
			rest = append(rest, arg)
		}
	}
	return rest, spec, nil
}

// Run executes the given command line arguments.
func (a *App) Run(args []string) error {
	args, spec, err := extractProviderFlag(args)
	if err != nil {
		return err
	}
	if spec == "" {
		spec = os.Getenv("AGENTLENS_PROVIDER")
	}
	if spec != "" && spec != "auto" {
		selected, err := providers.ResolveProvider(spec, a.Builtins)
		if err != nil {
			return err
		}
		a.Selected = selected
	}

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
		srv := server.NewServer(port, a.candidates()...)
		return srv.Start()
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

// CmdList lists all discovered sessions.
func (a *App) CmdList() error {
	sessions, err := collector.DiscoverAllSessions(a.candidates())
	if err != nil {
		return fmt.Errorf("failed to discover sessions: %w", err)
	}

	RenderSessionList(os.Stdout, sessions)
	return nil
}

// providerByID returns the candidate provider with the given ID.
func (a *App) providerByID(id string) *providers.LoadedProvider {
	for _, p := range a.candidates() {
		if p.Definition.Provider.ID == id {
			return p
		}
	}
	return nil
}

// resolveSessionOrFile maps a session ID (prefix) or trace file path to a session and its provider.
func (a *App) resolveSessionOrFile(target string) (*collector.SessionInfo, *providers.LoadedProvider, error) {
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		provider := a.Selected
		if provider == nil {
			provider = collector.DetectProvider(target, a.Builtins)
		}
		if provider == nil {
			return nil, nil, fmt.Errorf("could not detect the trace format of %q; pass --provider <id|path.yaml>", target)
		}
		return &collector.SessionInfo{
			SessionID:  filepath.Base(target),
			FilePath:   target,
			ProviderID: provider.Definition.Provider.ID,
			ModTime:    fi.ModTime(),
			SizeBytes:  fi.Size(),
		}, provider, nil
	}

	sessions, err := collector.DiscoverAllSessions(a.candidates())
	if err != nil {
		return nil, nil, err
	}

	if target == "" {
		if len(sessions) == 0 {
			return nil, nil, fmt.Errorf("no agent sessions found. Run `agentlens list` to view available sessions")
		}
		return &sessions[0], a.providerByID(sessions[0].ProviderID), nil
	}

	for _, s := range sessions {
		if s.SessionID == target || strings.HasPrefix(s.SessionID, target) {
			return &s, a.providerByID(s.ProviderID), nil
		}
	}

	return nil, nil, fmt.Errorf("session or file %q not found. Run `agentlens list` to view available sessions", target)
}

// analyzeSession ingests a session and returns its call graph with anomalies attached.
func analyzeSession(session *collector.SessionInfo, provider *providers.LoadedProvider) (*graph.Graph, error) {
	data, err := collector.NewCollector(provider).IngestSessionFile(session.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest session trace: %w", err)
	}

	g := graph.NewGraphBuilder().BuildWithTurns(session.SessionID, provider.Definition.Provider.ID, data.Turns, data.Nodes)
	detector.NewDetector(provider).Analyze(g)
	return g, nil
}

// CmdGraph visualizes the caller -> callee call graph for a session.
func (a *App) CmdGraph(targetID string) error {
	session, provider, err := a.resolveSessionOrFile(targetID)
	if err != nil {
		return err
	}
	g, err := analyzeSession(session, provider)
	if err != nil {
		return err
	}
	RenderCallGraph(os.Stdout, g)
	return nil
}

// CmdTrace visualizes the chronological turn-by-turn execution trace.
func (a *App) CmdTrace(targetID string) error {
	session, provider, err := a.resolveSessionOrFile(targetID)
	if err != nil {
		return err
	}
	g, err := analyzeSession(session, provider)
	if err != nil {
		return err
	}
	RenderExecutionTrace(os.Stdout, g)
	return nil
}

// CmdInspect displays detailed diagnostic information for a specific node ID.
func (a *App) CmdInspect(callID string) error {
	sessions, err := collector.DiscoverAllSessions(a.candidates())
	if err != nil {
		return err
	}

	for i := range sessions {
		g, err := analyzeSession(&sessions[i], a.providerByID(sessions[i].ProviderID))
		if err != nil {
			continue
		}

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
	matchedSession, provider, err := a.resolveSessionOrFile(targetID)
	if err != nil {
		return err
	}

	w := watcher.NewWatcher(provider, matchedSession.FilePath)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return w.Watch(ctx, os.Stdout)
}

func (a *App) PrintHelp() {
	fmt.Print(`AgentLens 🔍 — Generative AI Call Graph Visualizer (v0.1.0)

Usage:
  agentlens [--provider <id|path.yaml>] <command> [arguments]

Available Commands:
  list                 List recorded AI interaction sessions from all supported agents
  graph <session-id>   Render Caller ➔ Callee call graph tracing where Skills/MCPs were called
  trace <session-id>   Render turn-by-turn chronological execution trace tree
  watch [session-id]   Live stream tool calls, results & anomaly alerts from active session
  inspect <call-id>    Display detailed input, output, and anomaly diagnostics for a node
  ui                   Launch local Web UI dashboard
  version              Print version of agentlens
  help                 Print this help message

Global Flags:
  -p, --provider       Trace format: a built-in ID (antigravity, claude-code, cursor,
                       generic-jsonl), a provider YAML file, or "auto" (default).
                       Also settable via the AGENTLENS_PROVIDER environment variable.

Online Web Viewer:
  https://tsbkw.github.io/agentlens
`)
}
