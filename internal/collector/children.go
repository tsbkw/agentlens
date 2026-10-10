package collector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// ChildTrace is a subagent trace file found next to a session trace.
type ChildTrace struct {
	Path string
	// ParentCallID is the spawning call named by the child's sidecar, if any.
	ParentCallID string
	// ChildID is the child's own ID (source.child_traces.child_id_field), used to find the
	// spawning call through tool results when the sidecar names none.
	ChildID string
	// Name describes the child when no spawning call is linked (sidecar_name_field).
	Name string
}

// ResolveParent finds the call that spawned child: the call named by its sidecar, or else the
// call whose tool result names the child (links, child ID -> call ID). ok is false when no
// record links the child to a call present in byID.
func ResolveParent(child ChildTrace, byID map[string]models.CallNode, links map[string]string) (models.CallNode, models.ParentLink, bool) {
	if child.ParentCallID != "" {
		parent, ok := byID[child.ParentCallID]
		return parent, models.ParentLinkSidecar, ok
	}
	if child.ChildID != "" {
		if callID, linked := links[child.ChildID]; linked {
			parent, ok := byID[callID]
			return parent, models.ParentLinkResult, ok
		}
	}
	return models.CallNode{}, "", false
}

// IsUnlinked reports whether no record names the spawning call of child, so its placement can
// only be inferred.
func (child ChildTrace) IsUnlinked(links map[string]string) bool {
	if child.ParentCallID != "" {
		return false
	}
	_, linked := links[child.ChildID]
	return child.ChildID == "" || !linked
}

// InferredParent returns a placeholder subagent node for a child trace whose spawning call
// is not recorded. It is placed in the turn that was active when the child started.
func InferredParent(child ChildTrace, sessionID string, turnIndex int, start time.Time) models.CallNode {
	key := child.ChildID
	if key == "" {
		key = strings.TrimSuffix(filepath.Base(child.Path), filepath.Ext(child.Path))
	}
	name := child.Name
	if name == "" {
		name = key
	}
	return models.CallNode{
		ID:        "inferred-subagent-" + key,
		SessionID: sessionID,
		TurnIndex: turnIndex,
		Type:      models.NodeTypeSubagent,
		Name:      "Subagent",
		// The real caller is unknown too; like other top-level calls it is the main agent
		CallerScope: "Agent",
		ScopeName:   "Subagent: " + name,
		Timestamp:   start,
		Status:      models.StatusSuccess,
		ParentLink:  models.ParentLinkInferred,
		Arguments: map[string]interface{}{
			"child_trace": filepath.Base(child.Path),
			"note":        "No spawning call is recorded for this subagent; it is placed in the turn active when its trace started.",
		},
	}
}

// TurnAt returns the index of the last turn that started at or before ts (the first turn when
// ts precedes them all, 0 when there are no turns).
func TurnAt(turns []models.ExecutionTurn, ts time.Time) int {
	index := 0
	for i, t := range turns {
		if i == 0 || !t.Timestamp.After(ts) {
			index = t.Index
		}
	}
	return index
}

// attachChildTraces ingests the subagent trace files of filePath and appends their calls to
// data, nested under the spawning call (ParentID) and scope, recursively for nested subagents.
// Children with no recorded spawning call are attributed by time and marked as inferred.
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
	links := make(map[string]string, len(data.ChildLinks))
	for k, v := range data.ChildLinks {
		links[k] = v
	}
	add := func(nodes []models.CallNode, childLinks map[string]string) {
		for _, n := range nodes {
			if _, exists := byID[n.ID]; exists {
				// A forked child replays its parent's context, including the spawning call
				continue
			}
			byID[n.ID] = n
			data.Nodes = append(data.Nodes, n)
			if pos, ok := turnPos[n.TurnIndex]; ok {
				data.Turns[pos].Nodes = append(data.Turns[pos].Nodes, n)
			}
		}
		for k, v := range childLinks {
			if _, exists := links[k]; !exists {
				links[k] = v
			}
		}
	}

	pending := children
	for len(pending) > 0 {
		// Resolve children whose spawning call is known; repeat to reach nested subagents
		var next []ChildTrace
		progressed := false
		for _, child := range pending {
			parent, link, ok := ResolveParent(child, byID, links)
			if !ok {
				next = append(next, child)
				continue
			}
			nodes, childLinks, err := c.ingestChild(child.Path, parent, link)
			if err != nil {
				return err
			}
			add(nodes, childLinks)
			progressed = true
		}
		pending = next
		if progressed {
			continue
		}

		// Nothing links the rest to a call: place unlinked children by time, then retry the
		// others in case they were spawned from inside an unlinked child
		next = nil
		for _, child := range pending {
			if !child.IsUnlinked(links) {
				next = append(next, child) // linked to a call not present in this session
				continue
			}
			nodes, childLinks, err := c.ingestInferredChild(child, data)
			if err != nil {
				return err
			}
			add(nodes, childLinks)
			progressed = true
		}
		pending = next
		if !progressed {
			break // the remaining children belong to calls not present in this session
		}
	}
	return nil
}

// ingestChild assembles a subagent trace, attributing its calls to the spawning call.
func (c *Collector) ingestChild(path string, parent models.CallNode, link models.ParentLink) ([]models.CallNode, map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open child trace %q: %w", path, err)
	}
	defer file.Close()

	asm := NewChildAssembler(c.Provider, path, parent)
	if err := feedJSONL(asm, file); err != nil {
		return nil, nil, err
	}

	data := asm.Finish()
	AttributeToParent(data.Nodes, parent, link)
	return data.Nodes, data.ChildLinks, nil
}

// ingestInferredChild assembles a child trace with no recorded spawning call under a
// placeholder subagent node in the turn active when the child started.
func (c *Collector) ingestInferredChild(child ChildTrace, session *SessionData) ([]models.CallNode, map[string]string, error) {
	placeholder := InferredParent(child, session.SessionID, 0, time.Time{})
	nodes, links, err := c.ingestChild(child.Path, placeholder, models.ParentLinkInferred)
	if err != nil || len(nodes) == 0 {
		return nil, links, err
	}

	start := nodes[0].Timestamp
	for _, n := range nodes {
		if n.Timestamp.Before(start) {
			start = n.Timestamp
		}
	}
	placeholder = InferredParent(child, session.SessionID, TurnAt(session.Turns, start), start)
	AttributeToParent(nodes, placeholder, models.ParentLinkInferred)
	return append([]models.CallNode{placeholder}, nodes...), links, nil
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
// spawning call, nesting the subagent's top-level calls under it, and records how the child
// was linked to that call.
func AttributeToParent(nodes []models.CallNode, parent models.CallNode, link models.ParentLink) {
	for i := range nodes {
		nodes[i].SessionID = parent.SessionID
		nodes[i].TurnIndex = parent.TurnIndex
		if nodes[i].ParentID == "" {
			nodes[i].ParentID = parent.ID
		}
		nodes[i].ParentLink = link
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
		parentID, _ := providers.ResolveField(meta, cfg.ParentCallIDField).(string)
		children = append(children, ChildTrace{
			Path:         path,
			ParentCallID: parentID,
			ChildID:      childTraceID(path, cfg.ChildIDField),
			Name:         firstString(meta, cfg.SidecarNameField),
		})
	}
	return children, nil
}

// childTraceID reads the child's own ID from the first of its events that carries field.
func childTraceID(path, field string) string {
	if field == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for lines := 0; scanner.Scan() && lines < 100; lines++ {
		var event map[string]interface{}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if id, ok := providers.ResolveField(event, field).(string); ok && id != "" {
			return id
		}
	}
	return ""
}

// firstString returns the first non-empty string among the "a || b" fields of data.
func firstString(data map[string]interface{}, expr string) string {
	if expr == "" {
		return ""
	}
	for _, key := range strings.Split(expr, "||") {
		if s, ok := providers.ResolveField(data, strings.TrimSpace(key)).(string); ok && s != "" {
			return s
		}
	}
	return ""
}
