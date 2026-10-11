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
	"github.com/tsbkw/agentlens/internal/textutil"
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

// Watcher provides live stream monitoring of an active agent trace file. When the provider
// declares source.child_traces, subagent trace files are followed as they appear and their
// calls are shown under the call that spawned them.
type Watcher struct {
	Provider     *providers.LoadedProvider
	FilePath     string
	PollInterval time.Duration
	FromStart    bool
	// InferAfter is how long a subagent trace that names no spawning call waits for a tool
	// result linking it, before it is attributed to the current turn as inferred.
	InferAfter time.Duration
}

// NewWatcher initializes a Watcher instance.
func NewWatcher(provider *providers.LoadedProvider, filePath string) *Watcher {
	return &Watcher{
		Provider:     provider,
		FilePath:     filePath,
		PollInterval: 500 * time.Millisecond,
		FromStart:    false,
		InferAfter:   5 * time.Second,
	}
}

// tailSource is one followed trace file: the main session or a subagent.
type tailSource struct {
	path    string
	file    *os.File
	reader  *bufio.Reader
	partial string // bytes of a line whose newline has not been written yet
	asm     *collector.Assembler
	link    models.ParentLink // how a subagent trace was linked to parent
	child   collector.ChildTrace
	// replayed holds calls a forked subagent copied from its parent's context; they are
	// already shown and analyzed as the parent's calls.
	replayed map[string]bool
	parent   *models.CallNode // spawning call of a subagent trace; nil for the main trace
}

func openSource(path string, fromStart bool, asm *collector.Assembler, parent *models.CallNode) (*tailSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file %q: %w", path, err)
	}
	if !fromStart {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to seek trace file: %w", err)
		}
	}
	return &tailSource{path: path, file: file, reader: bufio.NewReader(file), asm: asm, parent: parent, replayed: make(map[string]bool)}, nil
}

// nextLine returns the next complete line, or ok=false when none has been written yet.
func (s *tailSource) nextLine() (string, bool, error) {
	chunk, err := s.reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			s.partial += chunk
			return "", false, nil
		}
		return "", false, fmt.Errorf("error reading trace stream: %w", err)
	}
	line := s.partial + chunk
	s.partial = ""
	return line, true, nil
}

// nodes returns the calls of this source, attributed to the spawning call for subagents.
func (s *tailSource) nodes() []models.CallNode {
	nodes := s.asm.Nodes()
	if s.parent == nil {
		return nodes
	}
	attributed := append([]models.CallNode(nil), nodes...)
	collector.AttributeToParent(attributed, *s.parent, s.link)
	return attributed
}

// graphNodes returns the calls of this source, without replayed ones, plus the placeholder
// parent of an inferred subagent.
func (s *tailSource) graphNodes() []models.CallNode {
	var nodes []models.CallNode
	if s.link == models.ParentLinkInferred {
		nodes = append(nodes, *s.parent)
	}
	for _, n := range s.nodes() {
		if !s.replayed[n.ID] {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// session is the set of followed trace files of one watched session.
type session struct {
	w         *Watcher
	out       io.Writer
	det       *detector.Detector
	sources   []*tailSource
	tracked   map[string]bool      // child trace paths already followed
	existing  map[string]bool      // child trace paths present before watching started (tail mode)
	firstSeen map[string]time.Time // when an unlinked child trace was first seen
}

// Watch continuously tails the trace file and streams tool calls, results, and alerts.
func (w *Watcher) Watch(ctx context.Context, out io.Writer) error {
	main, err := openSource(w.FilePath, w.FromStart, collector.NewAssembler(w.Provider, w.FilePath), nil)
	if err != nil {
		return err
	}
	sess := &session{
		w:         w,
		out:       out,
		det:       detector.NewDetector(w.Provider),
		sources:   []*tailSource{main},
		tracked:   make(map[string]bool),
		existing:  make(map[string]bool),
		firstSeen: make(map[string]time.Time),
	}
	defer sess.close()

	if !w.FromStart {
		// Subagents that were already running are tailed too, but their earlier calls are skipped
		children, _ := collector.FindChildTraces(w.Provider, w.FilePath)
		for _, child := range children {
			sess.existing[child.Path] = true
		}
	}

	fmt.Fprintf(out, "%s👀 Live watching trace: %s%s\n", colorBold, w.FilePath, colorReset)
	fmt.Fprintf(out, "%sStreaming tool calls and anomaly alerts. Press Ctrl+C to exit.%s\n\n", colorDim, colorReset)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "\nWatching stopped.")
			return nil
		default:
		}

		progressed := false
		// Sources may be appended while iterating, when a new subagent trace is discovered
		for i := 0; i < len(sess.sources); i++ {
			src := sess.sources[i]
			for {
				line, ok, err := src.nextLine()
				if err != nil {
					return err
				}
				if !ok {
					break
				}
				progressed = true
				sess.handleLine(src, line)
			}
		}

		if sess.discoverChildren() {
			continue
		}
		if !progressed {
			time.Sleep(w.PollInterval)
		}
	}
}

func (s *session) close() {
	for _, src := range s.sources {
		src.file.Close()
	}
}

// discoverChildren starts following subagent traces whose spawning call is known. It reports
// whether any new source was added.
func (s *session) discoverChildren() bool {
	children, err := collector.FindChildTraces(s.w.Provider, s.w.FilePath)
	if err != nil || len(children) == 0 {
		return false
	}

	byID := make(map[string]models.CallNode)
	links := make(map[string]string)
	for _, src := range s.sources {
		for _, n := range src.nodes() {
			byID[n.ID] = n
		}
		for k, v := range src.asm.ChildLinks() {
			if _, exists := links[k]; !exists {
				links[k] = v
			}
		}
	}

	// A record naming the spawning call of an inferred subagent may arrive after it was placed
	for _, src := range s.sources {
		if src.link != models.ParentLinkInferred {
			continue
		}
		if parent, link, ok := collector.ResolveParent(src.child, byID, links); ok {
			s.relink(src, parent, link)
		}
	}

	added := false
	for _, child := range children {
		if s.tracked[child.Path] {
			continue
		}
		preexisting := s.existing[child.Path]
		parent, link, known := collector.ResolveParent(child, byID, links)
		if !known {
			switch {
			case child.ParentCallID != "" && preexisting:
				// Spawned before watching started: the named spawning call was never read
				parent = models.CallNode{ID: child.ParentCallID, Name: "Agent"}
				link = models.ParentLinkSidecar
			case !child.IsUnlinked(links) && preexisting:
				// Likewise, for a child named by the result of a call that was never read
				parent = models.CallNode{ID: links[child.ChildID], Name: "Agent"}
				link = models.ParentLinkResult
			case child.IsUnlinked(links) && (preexisting || s.waitedForLink(child.Path)):
				// No record names the spawning call: attribute it to the current turn
				parent = collector.InferredParent(child, "", s.sources[0].asm.CurrentTurn(), time.Now())
				link = models.ParentLinkInferred
				fmt.Fprintf(s.out, "  ↳ %s[%s] spawning call not recorded; attributed to the current turn (inferred)%s\n",
					colorYellow, parent.ScopeName, colorReset)
			default:
				continue // the spawning call, or the result naming it, has not been read yet
			}
		}

		asm := collector.NewChildAssembler(s.w.Provider, child.Path, parent)
		src, err := openSource(child.Path, !preexisting, asm, &parent)
		if err != nil {
			continue
		}
		src.link = link
		src.child = child
		s.tracked[child.Path] = true
		s.sources = append(s.sources, src)
		added = true
	}
	return added
}

// relink attaches an inferred subagent trace to its spawning call once a record names it. Its
// calls seen so far move from the placeholder to that call and its scope.
func (s *session) relink(src *tailSource, parent models.CallNode, link models.ParentLink) {
	placeholder := src.parent.ScopeName
	src.asm.Rescope(collector.ChildScope(parent))
	src.parent = &parent
	src.link = link
	fmt.Fprintf(s.out, "  ↳ %s[%s] linked to its spawning call %s (%s); earlier calls moved from %s%s\n",
		colorCyan, collector.ChildScope(parent), parent.Name, link, placeholder, colorReset)
}

// knownElsewhere reports whether a call ID already belongs to another followed source.
func (s *session) knownElsewhere(src *tailSource, id string) bool {
	for _, other := range s.sources {
		if other == src {
			continue
		}
		for _, n := range other.asm.Nodes() {
			if n.ID == id && !other.replayed[id] {
				return true
			}
		}
	}
	return false
}

// waitedForLink reports whether an unlinked child trace has waited InferAfter for a tool
// result naming its spawning call.
func (s *session) waitedForLink(path string) bool {
	first, seen := s.firstSeen[path]
	if !seen {
		s.firstSeen[path] = time.Now()
		return s.w.InferAfter <= 0
	}
	return time.Since(first) >= s.w.InferAfter
}

func (s *session) handleLine(src *tailSource, line string) {
	out := s.out
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return
	}

	res, err := src.asm.Feed(payload)
	if err != nil {
		return
	}
	nodes := src.nodes()

	indent, linkNote := "", ""
	if src.parent != nil {
		indent = "  ↳ "
	}
	if src.link == models.ParentLinkInferred {
		linkNote = fmt.Sprintf(" %s(inferred parent)%s", colorDim, colorReset)
	}

	// A subagent's own prompt is the spawning call's input, not a user turn
	if res.TurnStarted && src.parent == nil {
		timeStr := time.Now().Format("15:04:05")
		fmt.Fprintf(out, "\n%s[%s] 💬 %sUSER PROMPT:%s %s\n", colorCyan, timeStr, colorBold, colorReset, res.Prompt)
	}

	for _, idx := range res.NewCalls {
		node := nodes[idx]
		if src.parent != nil && s.knownElsewhere(src, node.ID) {
			src.replayed[node.ID] = true
			continue
		}
		scope := node.CallerScope
		if node.ScopeName != "" {
			scope = node.ScopeName
		}
		timeStr := node.Timestamp.Format("15:04:05")
		typeBadge := formatTypeBadge(node.Type, node.MCPServer)
		scopeStr := fmt.Sprintf("%s[%s]%s", colorDim, scope, colorReset)
		fmt.Fprintf(out, "%s[%s] %s %s ➔ %s%s%s%s\n",
			indent, timeStr, scopeStr, typeBadge, colorBold, node.Name, colorReset, linkNote,
		)
	}

	if len(res.Results) == 0 {
		return
	}

	// Run live anomaly check over everything seen so far, subagents included
	var all []models.CallNode
	for _, other := range s.sources {
		all = append(all, other.graphNodes()...)
	}
	g := graph.NewGraphBuilder().Build("live-session", s.w.Provider.Definition.Provider.ID, all)
	anomalies := s.det.Analyze(g)

	for _, idx := range res.Results {
		node := nodes[idx]
		if src.replayed[node.ID] {
			continue
		}
		if node.Status == models.StatusFailed {
			timeStr := time.Now().Format("15:04:05")
			fmt.Fprintf(out, "%s  %s[%s] ✗ FAILED: %s (Error: %v)%s\n",
				indent, colorRed, timeStr, node.Name, failureText(node), colorReset,
			)
		}

		for _, a := range anomalies.Anomalies {
			if a.NodeID == node.ID || a.RelatedNodeID == node.ID {
				sevColor := colorYellow
				if a.Severity == models.SeverityCritical {
					sevColor = colorRed
				}
				fmt.Fprintf(out, "%s  %s🚨 %s: %s%s\n",
					indent, sevColor, a.Title, a.Description, colorReset,
				)
				if a.Recommendation != "" {
					fmt.Fprintf(out, "%s    %s💡 %s%s\n", indent, colorCyan, a.Recommendation, colorReset)
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
	return textutil.Truncate(text, 200, "...")
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
