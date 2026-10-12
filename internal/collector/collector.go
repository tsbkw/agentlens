package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	// ChildLinks maps child trace IDs to their spawning call IDs, as named by tool results.
	ChildLinks map[string]string `json:"-"`
	// SkippedLines counts lines that were not valid JSON objects or exceeded MaxLineBytes.
	SkippedLines int `json:"skipped_lines,omitempty"`
}

// MaxLineBytes is the longest JSONL line that is decoded; longer lines are skipped and counted.
const MaxLineBytes = 64 * 1024 * 1024

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
			// The same file reached through a symlinked directory is one session
			real := match
			if r, err := filepath.EvalSymlinks(match); err == nil {
				real = r
			}
			if seenPaths[real] {
				continue
			}
			seenPaths[real] = true

			fi, err := os.Stat(match)
			if err != nil || fi.IsDir() {
				continue
			}

			sessionID := c.Parser.ExtractSessionID(match, nil)
			if sessionID == "" {
				// Without a path rule, the file name tells sessions in one directory apart
				sessionID = strings.TrimSuffix(filepath.Base(match), filepath.Ext(match))
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

	data, err := c.IngestSessionReader(file, filePath)
	if err != nil {
		return nil, err
	}
	if err := c.attachChildTraces(filePath, data); err != nil {
		return nil, err
	}
	return data, nil
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
	asm := NewAssembler(c.Provider, filePath)
	skipped, err := feedJSONL(asm, reader)
	if err != nil {
		return nil, err
	}
	data := asm.Finish()
	data.SkippedLines = skipped
	return data, nil
}

// feedJSONL decodes JSONL events from reader into asm. Blank lines are ignored; lines that
// are not JSON objects or are longer than MaxLineBytes are skipped and counted, so one bad
// line never hides a whole session.
func feedJSONL(asm *Assembler, reader io.Reader) (skipped int, err error) {
	br := bufio.NewReaderSize(reader, 64*1024)
	first := true
	for {
		line, readErr := readLine(br, MaxLineBytes)
		if readErr != nil && readErr != io.EOF {
			if readErr == errLineTooLong {
				skipped++
				continue
			}
			return skipped, fmt.Errorf("error reading trace stream: %w", readErr)
		}
		if first {
			line = bytes.TrimPrefix(line, utf8BOM)
			first = false
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var payload map[string]interface{}
			if json.Unmarshal(trimmed, &payload) != nil {
				skipped++
			} else if _, err := asm.Feed(payload); err != nil {
				return skipped, err
			}
		}
		if readErr == io.EOF {
			return skipped, nil
		}
	}
}

var (
	utf8BOM        = []byte{0xEF, 0xBB, 0xBF}
	errLineTooLong = errors.New("line too long")
)

// readLine returns the next line without its newline. A line longer than max is consumed to
// its end and reported as errLineTooLong. io.EOF comes with the last line, which may be empty.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			// Drain the rest of the over-long line
			for err == bufio.ErrBufferFull {
				_, err = br.ReadSlice('\n')
			}
			if err != nil && err != bufio.ErrBufferFull {
				return nil, err
			}
			return nil, errLineTooLong
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return line, err
		}
		return bytes.TrimSuffix(line, []byte("\n")), nil
	}
}

// extractTurn reports whether payload starts a new user turn and returns its prompt and timestamp.
func extractTurn(cfg models.TurnExtraction, skip []*regexp.Regexp, payload map[string]interface{}) (string, time.Time, bool) {
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
	prompt := cleanPrompt(text, cfg.PromptTag)
	// Messages the provider marks as not written by the user: a local command's output,
	// an interruption marker (turns.skip_regex)
	for _, re := range skip {
		if re.MatchString(prompt) {
			return "", time.Time{}, false
		}
	}
	ts, _ := providers.ParseTimestampValue(providers.ResolveField(payload, cfg.TimestampField))
	return prompt, ts, true
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

// applyResult copies output and failure status from a result record onto its call node,
// reporting whether a result was found. flattenText reduces content-block outputs to plain text.
func applyResult(node *models.CallNode, cfg models.ResultExtraction, denied []*regexp.Regexp, result map[string]interface{}, flattenText bool) bool {
	output := providers.ResolveField(result, cfg.OutputField)
	if output == nil {
		return false
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
		text := providers.TextContent(output)
		for _, re := range denied {
			if re.MatchString(text) {
				node.Status = models.StatusDenied
				break
			}
		}
	}
	return true
}

// applyResultTiming derives the call duration from the result event timestamp.
func applyResultTiming(node *models.CallNode, cfg models.ResultExtraction, event map[string]interface{}) {
	if cfg.TimestampField == "" || node.DurationMs != 0 {
		return
	}
	ts, ok := providers.ParseTimestampValue(providers.ResolveField(event, cfg.TimestampField))
	if ok && ts.After(node.Timestamp) {
		node.DurationMs = ts.Sub(node.Timestamp).Milliseconds()
	}
}

// cleanPrompt extracts the inner text of tag when present. Otherwise it returns the first
// line the user wrote, skipping <tag>…</tag> blocks the harness injects around a prompt
// (reminders, pasted-content wrappers) when anything else remains.
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
	if line := firstLine(stripTagBlocks(content)); line != "" {
		return line
	}
	// Only tag blocks (a shell command the user ran, a local command's output): label the
	// turn with the first block's name and its first line of text
	if loc := tagOpenRe.FindStringSubmatchIndex(content); loc != nil {
		name := content[loc[2]:loc[3]]
		inner := content[loc[1]:]
		if end := strings.Index(inner, "</"+name+">"); end >= 0 {
			inner = inner[:end]
		}
		if line := firstLine(inner); line != "" {
			return "[" + name + "] " + line
		}
		return "[" + name + "]"
	}
	return firstLine(content)
}

func firstLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

var tagOpenRe = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9_-]*)[^<>]*>`)

// stripTagBlocks removes every <name …>…</name> block from content. A tag without a closing
// tag is ordinary text and stays.
func stripTagBlocks(content string) string {
	from := 0
	for {
		loc := tagOpenRe.FindStringSubmatchIndex(content[from:])
		if loc == nil {
			return content
		}
		start, tagEnd := from+loc[0], from+loc[1]
		closing := "</" + content[from+loc[2]:from+loc[3]] + ">"
		end := strings.Index(content[tagEnd:], closing)
		if end < 0 {
			from = tagEnd
			continue
		}
		content = content[:start] + content[tagEnd+end+len(closing):]
		from = start
	}
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

// FindSession returns the session whose ID is id, or else the one whose ID starts with it.
// A prefix shared by several sessions is an error that names them, so the wrong session is
// never analyzed silently. It returns nil when nothing matches.
func FindSession(sessions []SessionInfo, id string) (*SessionInfo, error) {
	var prefixed []*SessionInfo
	for i := range sessions {
		if sessions[i].SessionID == id {
			return &sessions[i], nil
		}
		if strings.HasPrefix(sessions[i].SessionID, id) {
			prefixed = append(prefixed, &sessions[i])
		}
	}
	switch len(prefixed) {
	case 0:
		return nil, nil
	case 1:
		return prefixed[0], nil
	}
	ids := make([]string, 0, len(prefixed))
	for _, s := range prefixed {
		ids = append(ids, s.SessionID+" ("+s.ProviderID+")")
	}
	return nil, fmt.Errorf("session ID %q is ambiguous; it matches %d sessions: %s", id, len(prefixed), strings.Join(ids, ", "))
}

// DiscoverAllSessions discovers sessions for every provider, most recently modified first.
func DiscoverAllSessions(candidates []*providers.LoadedProvider) ([]SessionInfo, error) {
	var all []SessionInfo
	for _, p := range candidates {
		sessions, err := NewCollector(p).DiscoverSessions()
		if err != nil {
			return nil, err
		}
		all = append(all, sessions...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].ModTime.After(all[j].ModTime)
	})
	return all, nil
}
