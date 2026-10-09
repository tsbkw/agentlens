package providers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tsbkw/agentlens/internal/models"
)

// DefaultScope is the caller scope of calls made directly by the main agent.
const DefaultScope = "Agent"

// CompiledScopeRule is a ScopeRule with its name regex pre-compiled.
type CompiledScopeRule struct {
	models.ScopeRule
	NameRegex *regexp.Regexp
}

// ScopeTracker attributes each tool call to its caller scope (Agent, Skill or Subagent)
// by applying the provider's declarative scope rules in call order.
type ScopeTracker struct {
	rules []CompiledScopeRule
	// Default is the scope of calls made directly by the traced agent (DefaultScope unless
	// the trace belongs to a subagent).
	Default string
	Active  string
}

// NewScopeTracker creates a ScopeTracker starting in the default Agent scope.
func NewScopeTracker(provider *LoadedProvider) *ScopeTracker {
	t := &ScopeTracker{Default: DefaultScope, Active: DefaultScope}
	if provider != nil {
		t.rules = provider.ScopeRules
	}
	return t
}

// Reset returns to the default scope, e.g. at the start of a new user turn.
func (t *ScopeTracker) Reset() {
	t.Active = t.Default
}

// Apply sets the node's CallerScope and, when a scope rule matches, its Type and ScopeName.
// It advances the active scope for subsequent calls according to the matched rule.
func (t *ScopeTracker) Apply(node *models.CallNode) {
	caller := t.Active
	for _, rule := range t.rules {
		name, ok := rule.match(node)
		if !ok {
			continue
		}
		enter := rule.EnterScope == nil || *rule.EnterScope

		switch rule.Kind {
		case models.ScopeKindSkill:
			node.Type = models.NodeTypeSkill
			if name != "" {
				node.ScopeName = "Skill: " + name
			}
		case models.ScopeKindSubagent:
			node.Type = models.NodeTypeSubagent
			// A subagent that takes over the flow is spawned by the enclosing agent, not by a skill
			if enter && strings.HasPrefix(t.Active, "Skill:") {
				t.Active = t.Default
				caller = t.Default
			}
			if name != "" {
				node.ScopeName = "Subagent: " + name
			}
		}

		if enter && node.ScopeName != "" {
			t.Active = node.ScopeName
		}
		break
	}
	node.CallerScope = caller
}

// match reports whether the rule applies to node and returns the extracted scope name.
func (r CompiledScopeRule) match(node *models.CallNode) (string, bool) {
	if !matchToolName(r.ToolName, node.Name) {
		return "", false
	}

	source := node.Name
	if r.NameField != "" {
		source = ""
		for _, key := range strings.Split(r.NameField, "||") {
			if val := ResolveField(node.Arguments, strings.TrimSpace(key)); val != nil {
				if str := fmt.Sprintf("%v", val); str != "" {
					source = str
					break
				}
			}
		}
	}

	if r.NameRegex == nil {
		return source, true
	}
	m := r.NameRegex.FindStringSubmatch(source)
	if m == nil {
		return "", false
	}
	if len(m) > 1 {
		return m[1], true
	}
	return m[0], true
}

func matchToolName(pattern, name string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}
