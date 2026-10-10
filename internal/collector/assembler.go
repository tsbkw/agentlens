package collector

import (
	"fmt"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// FeedResult describes what a single trace event contributed to the session.
type FeedResult struct {
	// TurnStarted is true when the event opened a new user turn with Prompt.
	TurnStarted bool
	Prompt      string
	// NewCalls are indices (into Assembler.Nodes) of tool calls introduced by the event.
	NewCalls []int
	// Results are indices of tool calls that received their result from the event.
	Results []int
}

// Assembler incrementally turns decoded trace events into turns and correlated call nodes,
// following the provider's declarative turn, result and scope rules. It is shared by
// batch ingestion and live watching.
type Assembler struct {
	provider *providers.LoadedProvider
	parser   *providers.TraceParser
	filePath string

	sessionID   string
	nodes       []models.CallNode
	turns       []models.ExecutionTurn
	turnNodeIdx [][]int // per turn, indices into nodes (materialized in Finish)
	turnNodes   []int
	stepNodeMap map[int]int    // step index -> index in nodes
	callNodeMap map[string]int // call ID -> index in nodes

	currentTurnIndex int
	currentPrompt    string
	currentTurnTime  time.Time
	scopes           *providers.ScopeTracker
	finished         bool
}

// NewAssembler creates an Assembler for events read from filePath.
func NewAssembler(provider *providers.LoadedProvider, filePath string) *Assembler {
	parser := providers.NewTraceParser(provider)
	return &Assembler{
		provider:        provider,
		parser:          parser,
		filePath:        filePath,
		sessionID:       parser.ExtractSessionID(filePath, nil),
		stepNodeMap:     make(map[int]int),
		callNodeMap:     make(map[string]int),
		currentTurnTime: time.Now(),
		scopes:          providers.NewScopeTracker(provider),
	}
}

// Nodes returns all call nodes assembled so far.
func (a *Assembler) Nodes() []models.CallNode {
	return a.nodes
}

// ActiveScope returns the scope subsequent calls will be attributed to.
func (a *Assembler) ActiveScope() string {
	return a.scopes.Active
}

// Feed processes one decoded trace event.
func (a *Assembler) Feed(payload map[string]interface{}) (FeedResult, error) {
	var res FeedResult
	extraction := a.provider.Definition.Extraction
	resultCfg := extraction.Results

	if a.sessionID == "" {
		a.sessionID = a.parser.ExtractSessionID(a.filePath, payload)
	}

	// Detect user interaction turn
	if prompt, ts, ok := extractTurn(extraction.Turns, payload); ok {
		a.flushTurn()
		a.currentTurnIndex++
		a.currentPrompt = prompt
		if !ts.IsZero() {
			a.currentTurnTime = ts
		}
		a.scopes.Reset()
		res.TurnStarted = true
		res.Prompt = prompt
	}

	// Extract tool call nodes if present
	nodes, err := a.parser.ParseNodes(a.sessionID, payload, a.currentTurnTime)
	if err != nil {
		return res, err
	}

	stepIdx := -1
	if resultCfg.StepIndexField != "" {
		if num, ok := providers.ResolveField(payload, resultCfg.StepIndexField).(float64); ok {
			stepIdx = int(num)
		}
	}

	for i := range nodes {
		nodes[i].TurnIndex = a.currentTurnIndex
		a.scopes.Apply(&nodes[i])

		a.nodes = append(a.nodes, nodes[i])
		idx := len(a.nodes) - 1
		a.turnNodes = append(a.turnNodes, idx)
		a.callNodeMap[nodes[i].ID] = idx
		if stepIdx >= 0 {
			a.stepNodeMap[stepIdx] = idx
		}
		res.NewCalls = append(res.NewCalls, idx)
	}

	// Correlate tool results back to their calls
	if resultCfg.Filter != "" && !providers.MatchesFilter(payload, resultCfg.Filter) {
		return res, nil
	}
	switch resultCfg.Correlation {
	case models.CorrelationPreviousStep:
		if stepIdx > 0 {
			if idx, exists := a.stepNodeMap[stepIdx-1]; exists && a.nodes[idx].Output == nil {
				if applyResult(&a.nodes[idx], resultCfg, payload, false) {
					res.Results = append(res.Results, idx)
				}
			}
		}
	case models.CorrelationCallID:
		for _, item := range resultItems(resultCfg, payload) {
			callID := fmt.Sprintf("%v", providers.ResolveField(item, resultCfg.CallIDField))
			if idx, exists := a.callNodeMap[callID]; exists {
				if applyResult(&a.nodes[idx], resultCfg, item, true) {
					applyResultTiming(&a.nodes[idx], resultCfg, payload)
					res.Results = append(res.Results, idx)
				}
			}
		}
	}

	return res, nil
}

// Finish closes the last turn and returns the assembled SessionData.
// The Assembler must not be fed after Finish.
func (a *Assembler) Finish() *SessionData {
	if a.finished {
		return &SessionData{SessionID: a.sessionID, Turns: a.turns, Nodes: a.nodes}
	}
	a.finished = true
	a.flushTurn()

	// Snapshot nodes per turn after all results have been correlated
	for t, indices := range a.turnNodeIdx {
		for _, idx := range indices {
			a.turns[t].Nodes = append(a.turns[t].Nodes, a.nodes[idx])
		}
	}

	return &SessionData{
		SessionID: a.sessionID,
		Turns:     a.turns,
		Nodes:     a.nodes,
	}
}

func (a *Assembler) flushTurn() {
	if a.currentTurnIndex == 0 && a.currentPrompt == "" {
		return
	}
	a.turns = append(a.turns, models.ExecutionTurn{
		Index:     a.currentTurnIndex,
		Prompt:    a.currentPrompt,
		Timestamp: a.currentTurnTime,
	})
	a.turnNodeIdx = append(a.turnNodeIdx, a.turnNodes)
	a.turnNodes = nil
}
