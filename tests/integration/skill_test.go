package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bundled Claude Code skill must stay a valid skill file: YAML frontmatter with a name
// matching its directory and a description that says when to use it, and it must name the
// MCP tools and CLI commands that exist.
func TestBundledSkillIsWellFormed(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "claude-code", "skills", "agentlens", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md must start with YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		t.Fatal("SKILL.md frontmatter is not closed")
	}
	front := text[4 : 4+end]
	if !strings.Contains(front, "name: agentlens\n") {
		t.Errorf("frontmatter must declare `name: agentlens`, got:\n%s", front)
	}
	if !strings.Contains(front, "description: Use when ") {
		t.Errorf("the description must tell the agent when to use the skill, got:\n%s", front)
	}
	for _, want := range []string{"`report`", "`why`", "`session`", "`inspect_call`", "agentlens why", "claude mcp add --scope user agentlens -- agentlens mcp", "Never print secrets"} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
}
