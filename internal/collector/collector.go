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

	var allNodes []models.CallNode
	var turns []models.ExecutionTurn
	var turnNodes []models.CallNode

	sessionID := c.Parser.ExtractSessionID(filePath, nil)
	stepNodeMap := make(map[int]*models.CallNode)

	currentTurnIndex := 0
	currentPrompt := ""
	currentTurnTime := time.Now()
	activeScope := "Agent"

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

		eventType := fmt.Sprintf("%v", payload["type"])

		// Detect user interaction turn
		if eventType == "USER_INPUT" {
			if currentTurnIndex > 0 || currentPrompt != "" {
				turns = append(turns, models.ExecutionTurn{
					Index:     currentTurnIndex,
					Prompt:    currentPrompt,
					Timestamp: currentTurnTime,
					Nodes:     turnNodes,
				})
				turnNodes = nil
			}
			currentTurnIndex++
			currentPrompt = cleanPrompt(fmt.Sprintf("%v", payload["content"]))
			if tsStr := fmt.Sprintf("%v", payload["created_at"]); tsStr != "" {
				if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
					currentTurnTime = t
				} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
					currentTurnTime = t
				}
			}
			activeScope = "Agent"
		}

		// Extract tool call nodes if present
		nodes, err := c.Parser.ParseNodes(sessionID, payload, currentTurnTime)
		if err != nil {
			return nil, err
		}

		stepIdx := -1
		if val := providers.ResolveField(payload, "step_index"); val != nil {
			if num, ok := val.(float64); ok {
				stepIdx = int(num)
			}
		}

		for i := range nodes {
			nodes[i].TurnIndex = currentTurnIndex

			// Detect Skill activation from viewing a skill instruction file (e.g. view_file on SKILL.md)
			if nodes[i].Name == "view_file" {
				if absPath, ok := nodes[i].Arguments["AbsolutePath"].(string); ok {
					if strings.Contains(absPath, "/skills/") && strings.HasSuffix(absPath, "/SKILL.md") {
						parts := strings.Split(absPath, "/skills/")
						if len(parts) > 1 {
							skillName := strings.Split(parts[1], "/")[0]
							activeScope = "Skill: " + skillName
						}
					}
				}
			} else if nodes[i].Name == "invoke_subagent" {
				nodes[i].Type = models.NodeTypeSubagent
				if role, ok := nodes[i].Arguments["Role"].(string); ok && role != "" {
					activeScope = "Subagent: " + role
				} else if typeName, ok := nodes[i].Arguments["TypeName"].(string); ok && typeName != "" {
					activeScope = "Subagent: " + typeName
				}
			} else if strings.HasPrefix(nodes[i].Name, "skill_") {
				nodes[i].Type = models.NodeTypeSkill
				activeScope = "Skill: " + strings.TrimPrefix(nodes[i].Name, "skill_")
			}

			nodes[i].CallerScope = activeScope
			allNodes = append(allNodes, nodes[i])
			turnNodes = append(turnNodes, nodes[i])

			if stepIdx >= 0 {
				stepNodeMap[stepIdx] = &allNodes[len(allNodes)-1]
			}
		}

		// Correlate results from subsequent output steps
		if stepIdx > 0 {
			if prevNode, exists := stepNodeMap[stepIdx-1]; exists && prevNode.Output == nil {
				if content := payload["content"]; content != nil {
					prevNode.Output = content
					if status := payload["status"]; status != nil {
						if strings.EqualFold(fmt.Sprintf("%v", status), "ERROR") {
							prevNode.Status = models.StatusFailed
						}
					}
				}
			}
		}
	}

	if currentTurnIndex > 0 || currentPrompt != "" {
		turns = append(turns, models.ExecutionTurn{
			Index:     currentTurnIndex,
			Prompt:    currentPrompt,
			Timestamp: currentTurnTime,
			Nodes:     turnNodes,
		})
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

func expandHomeDir(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
