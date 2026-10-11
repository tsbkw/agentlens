package providers

import (
	"path/filepath"
	"testing"

	"github.com/tsbkw/agentlens/internal/models"
)

func loadExampleProvider(t *testing.T, name string) *LoadedProvider {
	t.Helper()
	p, err := LoadProviderFromFile(filepath.Join("..", "..", "examples", "providers", name))
	if err != nil {
		t.Fatalf("Failed to load %s: %v", name, err)
	}
	return p
}

func TestScopeTrackerAntigravity(t *testing.T) {
	tracker := NewScopeTracker(loadExampleProvider(t, "antigravity.yaml"))

	// Path wrapped in JSON-escaped quotes (as emitted by Antigravity CLI)
	skillQuoted := models.CallNode{Name: "view_file", Arguments: map[string]interface{}{"AbsolutePath": "\"/x/skills/query-optimizer/SKILL.md\""}}
	tracker.Apply(&skillQuoted)
	if skillQuoted.Type != models.NodeTypeSkill || skillQuoted.ScopeName != "Skill: query-optimizer" || skillQuoted.CallerScope != "Agent" {
		t.Errorf("Unexpected skill node from quoted path: type=%v scope=%q caller=%q", skillQuoted.Type, skillQuoted.ScopeName, skillQuoted.CallerScope)
	}

	plainView := models.CallNode{Name: "view_file", Arguments: map[string]interface{}{"AbsolutePath": "\"/x/README.md\""}}
	tracker.Apply(&plainView)
	if plainView.ScopeName != "" || plainView.CallerScope != "Skill: query-optimizer" {
		t.Errorf("Plain view_file must not open a scope: scope=%q caller=%q", plainView.ScopeName, plainView.CallerScope)
	}

	// Subagent defined inside Subagents array (as in Antigravity invoke_subagent declaration)
	subArray := models.CallNode{
		Name: "invoke_subagent",
		Arguments: map[string]interface{}{
			"Subagents": []interface{}{
				map[string]interface{}{"Role": "\"Codebase Researcher\"", "TypeName": "researcher"},
			},
		},
	}
	tracker.Apply(&subArray)
	if subArray.CallerScope != "Agent" || subArray.ScopeName != "Subagent: Codebase Researcher" || tracker.Active != "Subagent: Codebase Researcher" {
		t.Errorf("Unexpected Subagents array attribution: caller=%q scope=%q active=%q", subArray.CallerScope, subArray.ScopeName, tracker.Active)
	}

	tracker.Reset()

	// A subagent spawned with direct TypeName
	sub := models.CallNode{Name: "invoke_subagent", Arguments: map[string]interface{}{"TypeName": "researcher"}}
	tracker.Apply(&sub)
	if sub.CallerScope != "Agent" || sub.ScopeName != "Subagent: researcher" || tracker.Active != "Subagent: researcher" {
		t.Errorf("Unexpected subagent attribution: caller=%q scope=%q active=%q", sub.CallerScope, sub.ScopeName, tracker.Active)
	}

	prefixed := models.CallNode{Name: "skill_code_search"}
	tracker.Apply(&prefixed)
	if prefixed.ScopeName != "Skill: code_search" || prefixed.CallerScope != "Subagent: researcher" {
		t.Errorf("Unexpected skill_* attribution: scope=%q caller=%q", prefixed.ScopeName, prefixed.CallerScope)
	}

	tracker.Reset()
	if tracker.Active != DefaultScope {
		t.Errorf("Expected Reset to return to %q, got %q", DefaultScope, tracker.Active)
	}
}

func TestScopeTrackerClaudeCode(t *testing.T) {
	tracker := NewScopeTracker(loadExampleProvider(t, "claude_code.yaml"))

	skill := models.CallNode{Name: "Skill", Arguments: map[string]interface{}{"skill": "incident-triage"}}
	tracker.Apply(&skill)
	if skill.Type != models.NodeTypeSkill || skill.ScopeName != "Skill: incident-triage" {
		t.Errorf("Unexpected Skill node: type=%v scope=%q", skill.Type, skill.ScopeName)
	}

	agent := models.CallNode{Name: "Agent", Arguments: map[string]interface{}{"description": "Inspect logs", "subagent_type": "Explore"}}
	tracker.Apply(&agent)
	if agent.Type != models.NodeTypeSubagent || agent.ScopeName != "Subagent: Explore" || agent.CallerScope != "Skill: incident-triage" {
		t.Errorf("Unexpected Agent node: type=%v scope=%q caller=%q", agent.Type, agent.ScopeName, agent.CallerScope)
	}
	// enter_scope: false keeps the main agent's next calls in the current scope
	if tracker.Active != "Skill: incident-triage" {
		t.Errorf("Expected active scope to stay on the skill, got %q", tracker.Active)
	}

	noType := models.CallNode{Name: "Agent", Arguments: map[string]interface{}{"description": "Summarize"}}
	tracker.Apply(&noType)
	if noType.ScopeName != "Subagent: Summarize" {
		t.Errorf("Expected name_field fallback to description, got %q", noType.ScopeName)
	}
}
