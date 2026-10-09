package collector

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"github.com/tsbkw/agentlens/internal/providers"
)

// detectSampleLines bounds how much of a trace file is read to detect its provider.
const detectSampleLines = 500

// DetectProvider picks the candidate whose extraction rules recognise the most tool calls
// and turns in the first lines of filePath. It returns nil when no candidate matches anything.
func DetectProvider(filePath string, candidates []*providers.LoadedProvider) *providers.LoadedProvider {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var events []map[string]interface{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() && len(events) < detectSampleLines {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err == nil {
			events = append(events, payload)
		}
	}

	var best *providers.LoadedProvider
	bestScore := 0
	for _, candidate := range candidates {
		asm := NewAssembler(candidate, filePath)
		for _, ev := range events {
			_, _ = asm.Feed(ev)
		}
		data := asm.Finish()
		if score := len(data.Nodes) + len(data.Turns); score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best
}
