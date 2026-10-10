package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// ChildTrace is a subagent trace file linked to the call that spawned it.
type ChildTrace struct {
	Path         string
	ParentCallID string
}

// attachChildTraces ingests the subagent trace files of filePath and appends their calls to
// data, nested under the spawning call (ParentID) and scope, recursively for nested subagents.
func (c *Collector) attachChildTraces(filePath string, data *SessionData) error {
	children, err := FindChildTraces(c.Provider, filePath)
	if err != nil || len(children) == 0 {
		return err
	}

	byID := make(map[string]models.CallNode, len(data.Nodes))
	for _, n := range data.Nodes {
		byID[n.ID] = n
	}
	turnPos := make(map[int]int, len(data.Turns))
	for i, t := range data.Turns {
		turnPos[t.Index] = i
	}

	// Resolve children whose spawning call is known; repeat to reach nested subagents
	pending := children
	for len(pending) > 0 {
		var next []ChildTrace
		for _, child := range pending {
			parent, ok := byID[child.ParentCallID]
			if !ok {
				next = append(next, child)
				continue
			}
			nodes, err := c.ingestChild(child.Path, parent)
			if err != nil {
				return err
			}
			for _, n := range nodes {
				byID[n.ID] = n
				data.Nodes = append(data.Nodes, n)
				if pos, ok := turnPos[n.TurnIndex]; ok {
					data.Turns[pos].Nodes = append(data.Turns[pos].Nodes, n)
				}
			}
		}
		if len(next) == len(pending) {
			break // the remaining children belong to calls not present in this session
		}
		pending = next
	}
	return nil
}

// ingestChild assembles a subagent trace, attributing its calls to the spawning call.
func (c *Collector) ingestChild(path string, parent models.CallNode) ([]models.CallNode, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open child trace %q: %w", path, err)
	}
	defer file.Close()

	asm := NewChildAssembler(c.Provider, path, parent)
	if err := feedJSONL(asm, file); err != nil {
		return nil, err
	}

	nodes := asm.Finish().Nodes
	AttributeToParent(nodes, parent)
	return nodes, nil
}

// NewChildAssembler creates an Assembler for a subagent trace whose own calls are attributed
// to the scope of the spawning call.
func NewChildAssembler(provider *providers.LoadedProvider, path string, parent models.CallNode) *Assembler {
	scope := parent.ScopeName
	if scope == "" {
		scope = "Subagent: " + parent.Name
	}
	asm := NewAssembler(provider, path)
	asm.SetDefaultScope(scope)
	return asm
}

// AttributeToParent places the calls of a subagent trace in the session and turn of the
// spawning call, nesting the subagent's top-level calls under it.
func AttributeToParent(nodes []models.CallNode, parent models.CallNode) {
	for i := range nodes {
		nodes[i].SessionID = parent.SessionID
		nodes[i].TurnIndex = parent.TurnIndex
		if nodes[i].ParentID == "" {
			nodes[i].ParentID = parent.ID
		}
	}
}

// FindChildTraces lists the subagent trace files of the trace at filePath, as configured by
// the provider's source.child_traces. It returns nothing when the provider has no such rule.
func FindChildTraces(provider *providers.LoadedProvider, filePath string) ([]ChildTrace, error) {
	cfg := provider.Definition.Source.ChildTraces
	if cfg == nil || cfg.PathGlob == "" {
		return nil, nil
	}

	dir := filepath.Dir(filePath)
	stem := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	pattern := strings.NewReplacer("{dir}", dir, "{stem}", stem).Replace(cfg.PathGlob)

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid child_traces.path_glob %q: %w", cfg.PathGlob, err)
	}

	var children []ChildTrace
	for _, path := range matches {
		sidecar := strings.TrimSuffix(path, filepath.Ext(path)) + cfg.SidecarSuffix
		raw, err := os.ReadFile(sidecar)
		if err != nil {
			continue
		}
		var meta map[string]interface{}
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		if id, ok := providers.ResolveField(meta, cfg.ParentCallIDField).(string); ok && id != "" {
			children = append(children, ChildTrace{Path: path, ParentCallID: id})
		}
	}
	return children, nil
}
