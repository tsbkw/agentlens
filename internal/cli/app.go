package cli

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/providers"
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
			return fmt.Errorf("missing session ID. Usage: agentlens graph <session-id>")
		}
		return a.CmdGraph(args[2])
	case "inspect":
		if len(args) < 3 {
			return fmt.Errorf("missing call ID. Usage: agentlens inspect <call-id>")
		}
		return a.CmdInspect(args[2])
	case "ui":
		fmt.Println("Starting AgentLens Web UI...")
		fmt.Println("Alternatively, access the free client-side viewer at: https://tsbkw.github.io/agentlens")
		return nil
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

// CmdGraph visualizes the call graph for a session.
func (a *App) CmdGraph(targetID string) error {
	col := collector.NewCollector(a.ActiveProvider)
	sessions, err := col.DiscoverSessions()
	if err != nil {
		return err
	}

	var matchedSession *collector.SessionInfo
	for _, s := range sessions {
		if s.SessionID == targetID || strings.HasPrefix(s.SessionID, targetID) {
			matchedSession = &s
			break
		}
	}

	if matchedSession == nil {
		return fmt.Errorf("session %q not found. Run `agentlens list` to view available sessions", targetID)
	}

	nodes, err := col.IngestFile(matchedSession.FilePath)
	if err != nil {
		return fmt.Errorf("failed to ingest session trace: %w", err)
	}

	builder := graph.NewGraphBuilder()
	g := builder.Build(matchedSession.SessionID, a.ActiveProvider.Definition.Provider.ID, nodes)

	// Run anomaly detection
	det := detector.NewDetector(a.ActiveProvider)
	det.Analyze(g)

	RenderCallGraph(os.Stdout, g)
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
		nodes, err := col.IngestFile(s.FilePath)
		if err != nil {
			continue
		}

		builder := graph.NewGraphBuilder()
		g := builder.Build(s.SessionID, a.ActiveProvider.Definition.Provider.ID, nodes)

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

func (a *App) PrintHelp() {
	fmt.Print(`AgentLens 🔍 — Generative AI Call Graph Visualizer (v0.1.0)

Usage:
  agentlens <command> [arguments]

Available Commands:
  list                 List recorded AI interaction sessions (Antigravity/Gemini default)
  graph <session-id>   Render call graph tree in terminal with status & anomaly badges
  inspect <call-id>    Display detailed input, output, and anomaly diagnostics for a node
  ui                   Launch local Web UI dashboard
  version              Print version of agentlens
  help                 Print this help message

Online Web Viewer:
  https://tsbkw.github.io/agentlens
`)
}
