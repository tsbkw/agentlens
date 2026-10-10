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

	skill := models.CallNode{Name: "view_file", Arguments: map[string]interface{}{"AbsolutePath": "/x/skills/query-optimizer/SKILL.md"}}
	tracker.Apply(&skill)
	if skill.Type != models.NodeTypeSkill || skill.ScopeName != "Skill: query-optimizer" || skill.CallerScope != "Agent" {
		t.Errorf("Unexpected skill node: type=%v scope=%q caller=%q", skill.Type, skill.ScopeName, skill.CallerScope)
	}

	plainView := models.CallNode{Name: "view_file", Arguments: map[string]interface{}{"AbsolutePath": "/x/README.md"}}
	tracker.Apply(&plainView)
	if plainView.ScopeName != "" || plainView.CallerScope != "Skill: query-optimizer" {
		t.Errorf("Plain view_file must not open a scope: scope=%q caller=%q", plainView.ScopeName, plainView.CallerScope)
	}

	// A subagent spawned while a skill is active belongs to the enclosing agent
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
