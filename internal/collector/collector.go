package collector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// SessionInfo contains metadata about a discovered session file on disk.
type SessionInfo struct {
	SessionID  string    `json:"session_id"`
	FilePath   string    `json:"file_path"`
	ProviderID string    `json:"provider_id"`
	ModTime    time.Time `json:"mod_time"`
	SizeBytes  int64     `json:"size_bytes"`
}

// SessionData groups parsed turns and all call nodes for a session.
type SessionData struct {
	SessionID string                 `json:"session_id"`
	Turns     []models.ExecutionTurn `json:"turns"`
	Nodes     []models.CallNode      `json:"nodes"`
}

// Collector reads trace logs and converts them into normalized events.
type Collector struct {
	Provider *providers.LoadedProvider
	Parser   *providers.TraceParser
}

// NewCollector creates a Collector for the given provider.
func NewCollector(provider *providers.LoadedProvider) *Collector {
	return &Collector{
		Provider: provider,
		Parser:   providers.NewTraceParser(provider),
	}
}

// DiscoverSessions searches disk for trace log files matching the provider's path patterns.
func (c *Collector) DiscoverSessions() ([]SessionInfo, error) {
	var sessions []SessionInfo
	seenPaths := make(map[string]bool)

	for _, pattern := range c.Provider.Definition.Source.PathPatterns {
		expanded := expandHomeDir(pattern)
		matches, err := filepath.Glob(expanded)
		if err != nil {
			continue
		}

		for _, match := range matches {
			if seenPaths[match] {
				continue
			}
			seenPaths[match] = true

			fi, err := os.Stat(match)
			if err != nil || fi.IsDir() {
				continue
			}

			sessionID := c.Parser.ExtractSessionID(match, nil)
			if sessionID == "" {
				sessionID = filepath.Base(filepath.Dir(match))
			}

			sessions = append(sessions, SessionInfo{
				SessionID:  sessionID,
				FilePath:   match,
				ProviderID: c.Provider.Definition.Provider.ID,
				ModTime:    fi.ModTime(),
				SizeBytes:  fi.Size(),
			})
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}

// IngestFile reads a single trace file and extracts all CallNodes.
func (c *Collector) IngestFile(filePath string) ([]models.CallNode, error) {
	data, err := c.IngestSessionFile(filePath)
	if err != nil {
		return nil, err
	}
	return data.Nodes, nil
}

// IngestSessionFile reads a trace file and returns full SessionData (turns and nodes).
func (c *Collector) IngestSessionFile(filePath string) (*SessionData, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file %q: %w", filePath, err)
	}
	defer file.Close()

	return c.IngestSessionReader(file, filePath)
}

// IngestReader parses JSONL events from an io.Reader stream into CallNodes.
func (c *Collector) IngestReader(reader io.Reader, filePath string) ([]models.CallNode, error) {
	data, err := c.IngestSessionReader(reader, filePath)
	if err != nil {
		return nil, err
	}
	return data.Nodes, nil
}

// IngestSessionReader parses JSONL events into full SessionData.
func (c *Collector) IngestSessionReader(reader io.Reader, filePath string) (*SessionData, error) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	extraction := c.Provider.Definition.Extraction
	turnCfg := extraction.Turns
	resultCfg := extraction.Results

	var allNodes []models.CallNode
	var turns []models.ExecutionTurn
	var turnNodeIdx [][]int // per turn, indices into allNodes (materialized at the end)
	var turnNodes []int

	sessionID := c.Parser.ExtractSessionID(filePath, nil)
	stepNodeMap := make(map[int]int)    // step index -> index in allNodes
	callNodeMap := make(map[string]int) // call ID -> index in allNodes

	currentTurnIndex := 0
	currentPrompt := ""
	currentTurnTime := time.Now()
	activeScope := "Agent"

	flushTurn := func() {
		if currentTurnIndex == 0 && currentPrompt == "" {
			return
		}
		turns = append(turns, models.ExecutionTurn{
			Index:     currentTurnIndex,
			Prompt:    currentPrompt,
			Timestamp: currentTurnTime,
		})
		turnNodeIdx = append(turnNodeIdx, turnNodes)
		turnNodes = nil
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}

		if sessionID == "" {
			sessionID = c.Parser.ExtractSessionID(filePath, payload)
		}

		// Detect user interaction turn
		if prompt, ts, ok := extractTurn(turnCfg, payload); ok {
			flushTurn()
			currentTurnIndex++
			currentPrompt = prompt
			if !ts.IsZero() {
				currentTurnTime = ts
			}
			activeScope = "Agent"
		}

		// Extract tool call nodes if present
		nodes, err := c.Parser.ParseNodes(sessionID, payload, currentTurnTime)
		if err != nil {
			return nil, err
		}

		stepIdx := -1
		if resultCfg.StepIndexField != "" {
			if num, ok := providers.ResolveField(payload, resultCfg.StepIndexField).(float64); ok {
				stepIdx = int(num)
			}
		}

		for i := range nodes {
			nodes[i].TurnIndex = currentTurnIndex

			callerForThisNode := activeScope

			// Detect Skill activation from viewing a skill instruction file (e.g. view_file on SKILL.md)
			if nodes[i].Name == "view_file" {
				if absPath, ok := nodes[i].Arguments["AbsolutePath"].(string); ok {
					if strings.Contains(absPath, "/skills/") && strings.HasSuffix(absPath, "/SKILL.md") {
						parts := strings.Split(absPath, "/skills/")
						if len(parts) > 1 {
							skillName := strings.Split(parts[1], "/")[0]
							nodes[i].Type = models.NodeTypeSkill
							activeScope = "Skill: " + skillName
						}
					}
				}
			} else if nodes[i].Name == "invoke_subagent" {
				nodes[i].Type = models.NodeTypeSubagent
				// If currently in a skill, invoke_subagent is initiated by the enclosing agent/subagent
				if strings.HasPrefix(activeScope, "Skill:") {
					activeScope = "Agent"
				}
				callerForThisNode = activeScope
				if role, ok := nodes[i].Arguments["Role"].(string); ok && role != "" {
					activeScope = "Subagent: " + role
				} else if typeName, ok := nodes[i].Arguments["TypeName"].(string); ok && typeName != "" {
					activeScope = "Subagent: " + typeName
				}
			} else if strings.HasPrefix(nodes[i].Name, "skill_") {
				nodes[i].Type = models.NodeTypeSkill
				activeScope = "Skill: " + strings.TrimPrefix(nodes[i].Name, "skill_")
			}

			nodes[i].CallerScope = callerForThisNode
			allNodes = append(allNodes, nodes[i])
			idx := len(allNodes) - 1
			turnNodes = append(turnNodes, idx)
			callNodeMap[nodes[i].ID] = idx

			if stepIdx >= 0 {
				stepNodeMap[stepIdx] = idx
			}
		}

		// Correlate tool results back to their calls
		if resultCfg.Filter != "" && !providers.MatchesFilter(payload, resultCfg.Filter) {
			continue
		}
		switch resultCfg.Correlation {
		case models.CorrelationPreviousStep:
			if stepIdx > 0 {
				if idx, exists := stepNodeMap[stepIdx-1]; exists && allNodes[idx].Output == nil {
					applyResult(&allNodes[idx], resultCfg, payload, false)
				}
			}
		case models.CorrelationCallID:
			for _, item := range resultItems(resultCfg, payload) {
				callID := fmt.Sprintf("%v", providers.ResolveField(item, resultCfg.CallIDField))
				if idx, exists := callNodeMap[callID]; exists {
					applyResult(&allNodes[idx], resultCfg, item, true)
					applyResultTiming(&allNodes[idx], resultCfg, payload)
				}
			}
		}
	}

	flushTurn()

	// Snapshot nodes per turn after all results have been correlated
	for t, indices := range turnNodeIdx {
		for _, idx := range indices {
			turns[t].Nodes = append(turns[t].Nodes, allNodes[idx])
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading trace stream: %w", err)
	}

	return &SessionData{
		SessionID: sessionID,
		Turns:     turns,
		Nodes:     allNodes,
	}, nil
}

// extractTurn reports whether payload starts a new user turn and returns its prompt and timestamp.
func extractTurn(cfg models.TurnExtraction, payload map[string]interface{}) (string, time.Time, bool) {
	if cfg.Filter == "" || !providers.MatchesFilter(payload, cfg.Filter) {
		return "", time.Time{}, false
	}
	raw := providers.ResolveField(payload, cfg.PromptField)
	text := providers.TextContent(raw)
	if text == "" {
		// A content-block array without text (e.g. only tool results) is not a prompt
		if _, isBlocks := raw.([]interface{}); isBlocks {
			return "", time.Time{}, false
		}
	}
	ts, _ := providers.ParseTimestamp(fmt.Sprintf("%v", providers.ResolveField(payload, cfg.TimestampField)))
	return cleanPrompt(text, cfg.PromptTag), ts, true
}

// resultItems returns the individual result records contained in a result event.
func resultItems(cfg models.ResultExtraction, payload map[string]interface{}) []map[string]interface{} {
	if cfg.ItemsPath == "" {
		return []map[string]interface{}{payload}
	}
	arr, ok := providers.ResolveField(payload, cfg.ItemsPath).([]interface{})
	if !ok {
		return nil
	}
	var items []map[string]interface{}
	for _, raw := range arr {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if cfg.ItemFilter != "" && !providers.MatchesFilter(item, cfg.ItemFilter) {
			continue
		}
		items = append(items, item)
	}
	return items
}

// applyResult copies output and failure status from a result record onto its call node.
// flattenText reduces content-block outputs to plain text.
func applyResult(node *models.CallNode, cfg models.ResultExtraction, result map[string]interface{}, flattenText bool) {
	output := providers.ResolveField(result, cfg.OutputField)
	if output == nil {
		return
	}
	if flattenText {
		if _, isBlocks := output.([]interface{}); isBlocks {
			output = providers.TextContent(output)
		}
	}
	node.Output = output

	failed := false
	if cfg.ErrorFlagField != "" {
		if flag, ok := providers.ResolveField(result, cfg.ErrorFlagField).(bool); ok && flag {
			failed = true
		}
	}
	if cfg.StatusField != "" {
		switch strings.ToLower(fmt.Sprintf("%v", providers.ResolveField(result, cfg.StatusField))) {
		case "error", "failed", "err":
			failed = true
		}
	}
	if failed {
		node.Status = models.StatusFailed
		if flattenText && node.ErrorMessage == "" {
			node.ErrorMessage = providers.TextContent(output)
		}
	}
}

// applyResultTiming derives the call duration from the result event timestamp.
func applyResultTiming(node *models.CallNode, cfg models.ResultExtraction, event map[string]interface{}) {
	if cfg.TimestampField == "" || node.DurationMs != 0 {
		return
	}
	ts, ok := providers.ParseTimestamp(fmt.Sprintf("%v", providers.ResolveField(event, cfg.TimestampField)))
	if ok && ts.After(node.Timestamp) {
		node.DurationMs = ts.Sub(node.Timestamp).Milliseconds()
	}
}

// cleanPrompt extracts the inner text of tag when present, otherwise the first line of content.
func cleanPrompt(content, tag string) string {
	if tag != "" {
		open, closing := "<"+tag+">", "</"+tag+">"
		if strings.Contains(content, open) && strings.Contains(content, closing) {
			start := strings.Index(content, open) + len(open)
			end := strings.Index(content, closing)
			if end > start {
				return strings.TrimSpace(content[start:end])
			}
		}
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return content
}

func expandHomeDir(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
