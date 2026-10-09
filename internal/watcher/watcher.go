package watcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// ANSI colors
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
)

// Watcher provides live stream monitoring of an active agent trace file.
type Watcher struct {
	Provider     *providers.LoadedProvider
	FilePath     string
	PollInterval time.Duration
	FromStart    bool
}

// NewWatcher initializes a Watcher instance.
func NewWatcher(provider *providers.LoadedProvider, filePath string) *Watcher {
	return &Watcher{
		Provider:     provider,
		FilePath:     filePath,
		PollInterval: 500 * time.Millisecond,
		FromStart:    false,
	}
}

// Watch continuously tails the trace file and streams tool calls, results, and alerts.
func (w *Watcher) Watch(ctx context.Context, out io.Writer) error {
	file, err := os.Open(w.FilePath)
	if err != nil {
		return fmt.Errorf("failed to open trace file %q: %w", w.FilePath, err)
	}
	defer file.Close()

	if !w.FromStart {
		// Seek to end of file to tail new events
		_, err = file.Seek(0, io.SeekEnd)
		if err != nil {
			return fmt.Errorf("failed to seek trace file: %w", err)
		}
	}

	fmt.Fprintf(out, "%s👀 Live watching trace: %s%s\n", colorBold, w.FilePath, colorReset)
	fmt.Fprintf(out, "%sStreaming tool calls and anomaly alerts. Press Ctrl+C to exit.%s\n\n", colorDim, colorReset)

	reader := bufio.NewReader(file)
	asm := collector.NewAssembler(w.Provider, w.FilePath)
	det := detector.NewDetector(w.Provider)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "\nWatching stopped.")
			return nil
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(w.PollInterval)
				continue
			}
			return fmt.Errorf("error reading trace stream: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}

		res, err := asm.Feed(payload)
		if err != nil {
			continue
		}
		nodes := asm.Nodes()

		if res.TurnStarted {
			timeStr := time.Now().Format("15:04:05")
			fmt.Fprintf(out, "\n%s[%s] 💬 %sUSER PROMPT:%s %s\n", colorCyan, timeStr, colorBold, colorReset, res.Prompt)
		}

		for _, idx := range res.NewCalls {
			node := nodes[idx]
			scope := node.CallerScope
			if node.ScopeName != "" {
				scope = node.ScopeName
			}
			timeStr := node.Timestamp.Format("15:04:05")
			typeBadge := formatTypeBadge(node.Type, node.MCPServer)
			scopeStr := fmt.Sprintf("%s[%s]%s", colorDim, scope, colorReset)
			fmt.Fprintf(out, "[%s] %s %s ➔ %s%s%s\n",
				timeStr, scopeStr, typeBadge, colorBold, node.Name, colorReset,
			)
		}

		if len(res.Results) == 0 {
			continue
		}

		// Run live anomaly check over everything seen so far
		g := graph.NewGraphBuilder().Build("live-session", w.Provider.Definition.Provider.ID, nodes)
		anomalies := det.Analyze(g)

		for _, idx := range res.Results {
			node := nodes[idx]
			if node.Status == models.StatusFailed {
				timeStr := time.Now().Format("15:04:05")
				fmt.Fprintf(out, "  %s[%s] ✗ FAILED: %s (Error: %v)%s\n",
					colorRed, timeStr, node.Name, failureText(node), colorReset,
				)
			}

			for _, a := range anomalies.Anomalies {
				if a.NodeID == node.ID || a.RelatedNodeID == node.ID {
					sevColor := colorYellow
					if a.Severity == models.SeverityCritical {
						sevColor = colorRed
					}
					fmt.Fprintf(out, "  %s🚨 %s: %s%s\n",
						sevColor, a.Title, a.Description, colorReset,
					)
					if a.Recommendation != "" {
						fmt.Fprintf(out, "    %s💡 %s%s\n", colorCyan, a.Recommendation, colorReset)
					}
				}
			}
		}
	}
}

// failureText returns a single-line, bounded description of why a call failed.
func failureText(node models.CallNode) string {
	text := node.ErrorMessage
	if text == "" {
		text = providers.TextContent(node.Output)
	}
	text = strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return text
}

func formatTypeBadge(t models.CallNodeType, mcpServer string) string {
	switch t {
	case models.NodeTypeMCPTool:
		srv := ""
		if mcpServer != "" {
			srv = ":" + mcpServer
		}
		return fmt.Sprintf("%s[MCP%s]%s", colorCyan, srv, colorReset)
	case models.NodeTypeSkill:
		return fmt.Sprintf("%s[Skill]%s", colorGreen, colorReset)
	case models.NodeTypeSubagent:
		return fmt.Sprintf("%s[Subagent]%s", colorMagenta, colorReset)
	default:
		return fmt.Sprintf("%s[System]%s", colorDim, colorReset)
	}
}
