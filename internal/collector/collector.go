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
	SessionID   string    `json:"session_id"`
	FilePath    string    `json:"file_path"`
	ProviderID  string    `json:"provider_id"`
	ModTime     time.Time `json:"mod_time"`
	SizeBytes   int64     `json:"size_bytes"`
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

	// Sort most recently modified first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}

// IngestFile reads a single trace file and extracts all CallNodes.
func (c *Collector) IngestFile(filePath string) ([]models.CallNode, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file %q: %w", filePath, err)
	}
	defer file.Close()

	return c.IngestReader(file, filePath)
}

// IngestReader parses JSONL events from an io.Reader stream.
func (c *Collector) IngestReader(reader io.Reader, filePath string) ([]models.CallNode, error) {
	scanner := bufio.NewScanner(reader)
	// Allow up to 10MB per line for large AI tool outputs/transcripts
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var allNodes []models.CallNode
	sessionID := c.Parser.ExtractSessionID(filePath, nil)
	lineNumber := 0

	// Step index to node map for correlating results/outputs from subsequent steps
	stepNodeMap := make(map[int]*models.CallNode)

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}

		// Try extracting session ID from payload if not found in path
		if sessionID == "" {
			sessionID = c.Parser.ExtractSessionID(filePath, payload)
		}

		// Extract tool call nodes if present
		nodes, err := c.Parser.ParseNodes(sessionID, payload, time.Now())
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
			allNodes = append(allNodes, nodes[i])
			if stepIdx >= 0 {
				stepNodeMap[stepIdx] = &allNodes[len(allNodes)-1]
			}
		}

		// Correlate execution outputs from subsequent steps (e.g. Antigravity GENERIC or result steps)
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

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading trace stream: %w", err)
	}

	return allNodes, nil
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
