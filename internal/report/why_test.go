package report

import (
	"strings"
	"testing"
	"time"
)

func TestDiagnoseChoosesReasonAndEvidence(t *testing.T) {
	day := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ex   Explanation
		want string
		text []string
	}{
		{"not found", Explanation{Query: "githb", Suggestions: []string{"github"}}, DiagnosisNotFound, []string{"Similar names seen: github"}},
		{"connection", Explanation{Tool: ToolHealth{Key: "jira", Kind: KindMCPServer, Reason: ReasonConnectionFailing, SessionsFailedConnect: 3, LastConnectError: "HTTP 401"}}, ReasonConnectionFailing, []string{"failed to connect to the server in 3 sessions", "HTTP 401"}},
		{"auth", Explanation{Tool: ToolHealth{Key: "github", Kind: KindMCPServer, Reason: ReasonAuthUnresolved, Counts: Counts{Calls: 4, Failed: 2}, LastSuccess: day, SessionsSinceLastSuccess: 5, LastError: "Bad credentials", LastErrorClass: ErrorClassAuth, FallbacksFrom: 2, LastFallbackTo: "Bash"}},
			ReasonAuthUnresolved, []string{"4 calls: 2 succeeded, 2 failed, 0 denied", "5 sessions since", "Last call error (auth): Bad credentials", "2 silent fallbacks", "to Bash"}},
		{"unused with substitute", Explanation{Service: "GitHub", Tool: ToolHealth{Key: "github", Kind: KindMCPServer, Reason: ReasonUnused, SessionsOffered: 6},
			Substitutes: []SubstituteUse{{Tool: "Bash", Example: "gh pr view 1", Sessions: 4, SessionsWhileAbsent: 4}}}, DiagnosisSubstitutePreferred, []string{"Bash reached GitHub instead in 4 sessions", "e.g. gh pr view 1"}},
		{"unused without substitute", Explanation{Tool: ToolHealth{Key: "deploy", Kind: KindSkill, Reason: ReasonUnused, SessionsOffered: 2}}, DiagnosisNoDemand, []string{"Offered in 2 sessions"}},
		{"healthy", Explanation{Tool: ToolHealth{Key: "slack", Kind: KindMCPServer, Counts: Counts{Calls: 3}, LastSuccess: day}}, DiagnosisHealthy, nil},
		{"healthy but bypassed", Explanation{Service: "GitHub", Tool: ToolHealth{Key: "github", Kind: KindMCPServer, Counts: Counts{Calls: 1}, LastSuccess: day},
			Substitutes: []SubstituteUse{{Tool: "Bash", Sessions: 5, SessionsWhileHealthy: 1, SessionsWhileAbsent: 3, SessionsWhileFailing: 1}}}, DiagnosisSubstitutePreferred, []string{"even when the tool was available"}},
	}
	for _, c := range cases {
		d := Diagnose(&c.ex)
		if d.Reason != c.want {
			t.Errorf("%s: reason %q, want %q (%s)", c.name, d.Reason, c.want, d.Summary)
		}
		if d.Summary == "" || d.Recommendation == "" {
			t.Errorf("%s: summary and recommendation must be set: %+v", c.name, d)
		}
		all := strings.Join(d.Evidence, "\n") + "\n" + d.Summary
		for _, want := range c.text {
			if !strings.Contains(all, want) {
				t.Errorf("%s: evidence lacks %q:\n%s", c.name, want, all)
			}
		}
	}
}

func TestBuildPromptNamesTheToolAndTheChecks(t *testing.T) {
	day := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	ex := &Explanation{Query: "github", Window: Window{Since: day.AddDate(0, 0, -30)},
		Tool:     ToolHealth{Key: "github", Kind: KindMCPServer, Reason: ReasonAuthUnresolved, LastError: "Bad credentials"},
		Sessions: []SessionOutcome{{SessionID: "abcdef1234", Start: day, Calls: 1, Failed: 1, FirstError: "Bad credentials", FallbackTo: []string{"Bash"}}}}
	ex.Diagnosis = Diagnose(ex)
	p := BuildPrompt(ex)
	for _, want := range []string{
		"MCP server `github`", "Diagnosis: authentication failed and not fixed", "session abcdef12", "1 failed", "fell back to Bash",
		"claude mcp get github", "/mcp", "agentlens why github --since 1d", "Never print secrets",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	skill := &Explanation{Query: "deploy", Tool: ToolHealth{Key: "deploy", Kind: KindSkill, Reason: ReasonUnused, SessionsOffered: 3}}
	skill.Diagnosis = Diagnose(skill)
	if sp := BuildPrompt(skill); !strings.Contains(sp, "~/.claude/skills/deploy/SKILL.md") || !strings.Contains(sp, "Skill `deploy`") {
		t.Errorf("skill prompt lacks the skill checks:\n%s", sp)
	}
	notFound := &Explanation{Query: "ghub"}
	notFound.Diagnosis = Diagnose(notFound)
	if np := BuildPrompt(notFound); !strings.Contains(np, "`ghub`") {
		t.Errorf("not-found prompt must still name the query:\n%s", np)
	}
}

func TestSessionOutcomeText(t *testing.T) {
	cases := map[string]SessionOutcome{
		"server failed to connect": {ConnectFailed: true},
		"waiting for sign-in":      {Pending: true},
		"offered, not called":      {Offered: true},
		"absent":                   {},
		"2 ok":                     {Calls: 2, Success: 2},
		"1 ok, 2 failed, 1 denied": {Calls: 4, Success: 1, Failed: 2, Denied: 1},
	}
	for want, s := range cases {
		if got := s.Outcome(); got != want {
			t.Errorf("Outcome() = %q, want %q", got, want)
		}
	}
}
