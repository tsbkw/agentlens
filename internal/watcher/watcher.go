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
	Parser       *providers.TraceParser
	FilePath     string
	PollInterval time.Duration
	FromStart    bool
}

// NewWatcher initializes a Watcher instance.
func NewWatcher(provider *providers.LoadedProvider, filePath string) *Watcher {
	return &Watcher{
		Provider:     provider,
		Parser:       providers.NewTraceParser(provider),
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
	stepNodeMap := make(map[int]*models.CallNode)
	var activeScope string = "Agent"
	var allNodes []models.CallNode
	builder := graph.NewGraphBuilder()
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

		// Handle user input turn
		if fmt.Sprintf("%v", payload["type"]) == "USER_INPUT" {
			prompt := cleanPrompt(fmt.Sprintf("%v", payload["content"]))
			timeStr := time.Now().Format("15:04:05")
			fmt.Fprintf(out, "\n%s[%s] 💬 %sUSER PROMPT:%s %s\n", colorCyan, timeStr, colorBold, colorReset, prompt)
			activeScope = "Agent"
			continue
		}

		// Parse tool calls
		stepIdx := -1
		if s, ok := payload["step_index"].(float64); ok {
			stepIdx = int(s)
		}

		nodes, err := w.Parser.ParseNodes("live-session", payload, time.Now())
		if err == nil && len(nodes) > 0 {
			for i := range nodes {
				node := &nodes[i]
				// Scope detection
				if node.Name == "view_file" {
					if absPath, ok := node.Arguments["AbsolutePath"].(string); ok {
						if strings.Contains(absPath, "/skills/") && strings.HasSuffix(absPath, "/SKILL.md") {
							parts := strings.Split(absPath, "/skills/")
							if len(parts) > 1 {
								activeScope = "Skill: " + strings.Split(parts[1], "/")[0]
							}
						}
					}
				} else if node.Name == "invoke_subagent" {
					node.Type = models.NodeTypeSubagent
					if role, ok := node.Arguments["Role"].(string); ok && role != "" {
						activeScope = "Subagent: " + role
					}
				} else if strings.HasPrefix(node.Name, "skill_") {
					node.Type = models.NodeTypeSkill
					activeScope = "Skill: " + strings.TrimPrefix(node.Name, "skill_")
				}

				node.CallerScope = activeScope
				allNodes = append(allNodes, *node)
				if stepIdx >= 0 {
					stepNodeMap[stepIdx] = node
				}

				timeStr := node.Timestamp.Format("15:04:05")
				typeBadge := formatTypeBadge(node.Type, node.MCPServer)
				scopeStr := fmt.Sprintf("%s[%s]%s", colorDim, activeScope, colorReset)
				fmt.Fprintf(out, "[%s] %s %s ➔ %s%s%s\n",
					timeStr, scopeStr, typeBadge, colorBold, node.Name, colorReset,
				)
			}
		}

		// Correlate result / output
		if stepIdx > 0 {
			if prevNode, exists := stepNodeMap[stepIdx-1]; exists && prevNode.Output == nil {
				if content := payload["content"]; content != nil {
					prevNode.Output = content
				}
				if status := payload["status"]; status == "ERROR" {
					prevNode.Status = models.StatusFailed
					if errStr, ok := payload["content"].(string); ok {
						prevNode.ErrorMessage = errStr
					}
					timeStr := time.Now().Format("15:04:05")
					fmt.Fprintf(out, "  %s[%s] ✗ FAILED: %s (Error: %v)%s\n",
						colorRed, timeStr, prevNode.Name, prevNode.ErrorMessage, colorReset,
					)
				}

				// Run live anomaly check
				g := builder.Build("live-session", w.Provider.Definition.Provider.ID, allNodes)
				anomalies := det.Analyze(g)
				for _, a := range anomalies.Anomalies {
					if a.NodeID == prevNode.ID || a.RelatedNodeID == prevNode.ID {
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

func cleanPrompt(content string) string {
	if strings.Contains(content, "<USER_REQUEST>") && strings.Contains(content, "</USER_REQUEST>") {
		start := strings.Index(content, "<USER_REQUEST>") + len("<USER_REQUEST>")
		end := strings.Index(content, "</USER_REQUEST>")
		if end > start {
			return strings.TrimSpace(content[start:end])
		}
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return content
}
