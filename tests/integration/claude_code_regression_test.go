package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/report"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// The Claude Code regression fixture (tests/fixtures/synthetic/claude_code_regression*) is a
// synthetic session shaped like real transcripts: parallel tool calls, a Skill scope, nested
// and forked subagents, a retry loop, high-confidence fallbacks, denied calls, compaction,
// an interrupted response, a timed-out command and a truncated last line. The golden file
// records everything the pipeline derives from it. Regenerate it after an intended change
// with UPDATE_GOLDEN=1 and review the diff.
const (
	ccFixtureStem = "claude_code_regression"
	ccSessionID   = "7e3f9a2c-4b1d-4c8e-9f6a-0d2b5c7e8a91"
	ccProjectDir  = "-home-dev-workspace-shop-api"
)

type ccGolden struct {
	Provider     string                 `json:"provider"`
	SkippedLines int                    `json:"skipped_lines"`
	Turns        []ccTurn               `json:"turns"`
	Nodes        []ccNode               `json:"nodes"`
	Anomalies    []ccAnomaly            `json:"anomalies"`
	Dependencies []ccDependency         `json:"dependencies"`
	Metrics      map[string]interface{} `json:"metrics"`
	Report       ccReport               `json:"report"`
	Availability *models.Availability   `json:"availability,omitempty"`
}

type ccTurn struct {
	Index  int      `json:"index"`
	Prompt string   `json:"prompt"`
	Calls  []string `json:"calls"`
}

type ccNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	MCPServer   string `json:"mcp_server,omitempty"`
	Status      string `json:"status"`
	TurnIndex   int    `json:"turn_index"`
	CallerScope string `json:"caller_scope"`
	ScopeName   string `json:"scope_name,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	ParentLink  string `json:"parent_link,omitempty"`
	Auxiliary   bool   `json:"auxiliary,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	OutputHead  string `json:"output_head,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ccAnomaly struct {
	Type          string `json:"type"`
	NodeID        string `json:"node_id"`
	RelatedNodeID string `json:"related_node_id,omitempty"`
	Confidence    string `json:"confidence,omitempty"`
	Title         string `json:"title"`
}

type ccDependency struct {
	Caller     string         `json:"caller"`
	Callee     string         `json:"callee"`
	Calls      int            `json:"calls"`
	Success    int            `json:"success"`
	Failed     int            `json:"failed"`
	Denied     int            `json:"denied,omitempty"`
	Fallback   bool           `json:"fallback,omitempty"`
	FallbackTo string         `json:"fallback_to,omitempty"`
	Confidence string         `json:"confidence,omitempty"`
	Inferred   bool           `json:"inferred,omitempty"`
	Children   []ccDependency `json:"children,omitempty"`
}

type ccReport struct {
	Sessions int           `json:"sessions"`
	Total    report.Counts `json:"total"`
	Session  report.Row    `json:"session"`
}

func TestClaudeCodeRegressionFixture(t *testing.T) {
	builtins, err := providers.LoadBuiltinProviders()
	if err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join("..", "fixtures", "synthetic")
	fixturePath := filepath.Join(fixtureDir, ccFixtureStem+".jsonl")
	goldenPath := filepath.Join(fixtureDir, ccFixtureStem+"_golden.json")

	provider := collector.DetectProvider(fixturePath, builtins)
	if provider == nil {
		t.Fatal("No built-in provider recognised the fixture")
	}

	data, err := collector.NewCollector(provider).IngestSessionFile(fixturePath)
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	g := graph.NewGraphBuilder().BuildWithTurns(data.SessionID, provider.Definition.Provider.ID, data.Turns, data.Nodes)
	result := detector.NewDetector(provider).Analyze(g)

	got := ccGolden{Provider: provider.Definition.Provider.ID, SkippedLines: data.SkippedLines, Availability: data.Availability}
	for _, turn := range data.Turns {
		calls := []string{}
		for _, n := range turn.Nodes {
			calls = append(calls, n.ID)
		}
		got.Turns = append(got.Turns, ccTurn{Index: turn.Index, Prompt: turn.Prompt, Calls: calls})
	}
	for _, n := range data.Nodes {
		got.Nodes = append(got.Nodes, ccNode{
			ID: n.ID, Name: n.Name, Type: string(n.Type), MCPServer: n.MCPServer, Status: string(n.Status),
			TurnIndex: n.TurnIndex, CallerScope: n.CallerScope, ScopeName: n.ScopeName,
			ParentID: n.ParentID, ParentLink: string(n.ParentLink), Auxiliary: n.Auxiliary, DurationMs: n.DurationMs,
			OutputHead: textutil.TruncateHead(firstTextLine(providers.TextContent(n.Output)), 60, "…"),
			Error:      n.ErrorMessage,
		})
	}
	for _, a := range result.Anomalies {
		got.Anomalies = append(got.Anomalies, ccAnomaly{Type: string(a.Type), NodeID: a.NodeID, RelatedNodeID: a.RelatedNodeID, Confidence: string(a.Confidence), Title: a.Title})
	}
	sort.Slice(got.Anomalies, func(i, j int) bool {
		a, b := got.Anomalies[i], got.Anomalies[j]
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.RelatedNodeID < b.RelatedNodeID
	})
	got.Dependencies = convertDependencies(g.Dependencies)
	got.Metrics = toJSONMap(t, g.ComputeMetrics())
	got.Report = collectReport(t, builtins, fixtureDir)

	actual, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, actual, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden file updated: %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("Read golden: %v (run with UPDATE_GOLDEN=1 to create it)", err)
	}
	if !bytes.Equal(want, actual) {
		t.Errorf("The pipeline output differs from %s.\n%s\nIf the change is intended, run: UPDATE_GOLDEN=1 go test ./tests/integration -run TestClaudeCodeRegressionFixture",
			goldenPath, firstDifferences(string(want), string(actual), 12))
	}
}

// collectReport runs the report over a fake home directory holding the fixture as a real
// Claude Code session, which exercises discovery, child trace lookup and aggregation.
func collectReport(t *testing.T, builtins []*providers.LoadedProvider, fixtureDir string) ccReport {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, ".claude", "projects", ccProjectDir)
	copyTree(t, filepath.Join(fixtureDir, ccFixtureStem+".jsonl"), filepath.Join(project, ccSessionID+".jsonl"))
	copyTree(t, filepath.Join(fixtureDir, ccFixtureStem), filepath.Join(project, ccSessionID))

	window, err := report.ParseWindow("2026-10-01", "2026-10-31", time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := report.Collect(builtins, window, "", nil)
	if err != nil {
		t.Fatalf("report.Collect failed: %v", err)
	}
	out := ccReport{Sessions: rep.Sessions, Total: rep.Total}
	if len(rep.BySession) == 1 {
		out.Session = rep.BySession[0]
	} else {
		t.Errorf("Expected exactly the fixture session in the report, got %d rows", len(rep.BySession))
	}
	return out
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		copyTree(t, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
	}
}

func convertDependencies(deps []models.CallerDependency) []ccDependency {
	out := make([]ccDependency, 0, len(deps))
	for _, d := range deps {
		out = append(out, ccDependency{
			Caller: d.Caller, Callee: d.Callee, Calls: d.CallCount, Success: d.SuccessCount, Failed: d.FailCount, Denied: d.DeniedCount,
			Fallback: d.IsFallback, FallbackTo: d.FallbackTo, Confidence: string(d.FallbackConfidence), Inferred: d.Inferred,
			Children: convertDependencies(d.Children),
		})
	}
	return out
}

func toJSONMap(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func firstTextLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// firstDifferences lists the first differing lines of two texts, with their line numbers.
func firstDifferences(want, got string, max int) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; (i < len(w) || i < len(g)) && shown < max; i++ {
		var a, c string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			c = g[i]
		}
		if a == c {
			continue
		}
		fmt.Fprintf(&b, "line %d:\n  golden: %s\n  actual: %s\n", i+1, a, c)
		shown++
	}
	return b.String()
}
