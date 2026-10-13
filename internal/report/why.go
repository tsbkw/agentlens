package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// Diagnoses of `agentlens why`, beyond the dormancy reasons of ToolHealth.
const (
	// DiagnosisHealthy: the tool works and was used recently.
	DiagnosisHealthy = "healthy"
	// DiagnosisSubstitutePreferred: the tool was available but the agent reached the same
	// service another way (CLI, HTTP) instead.
	DiagnosisSubstitutePreferred = "substitute_preferred"
	// DiagnosisNoDemand: offered and healthy, but nothing in the window needed it.
	DiagnosisNoDemand = "no_demand"
	// DiagnosisNotFound: the name matches no Skill or MCP server seen or offered in the window.
	DiagnosisNotFound = "not_found"
)

// Explanation is the answer of `agentlens why <tool>`: how one Skill or MCP server fared in
// every session of the window, what replaced it, and the most likely reason it is not used.
type Explanation struct {
	Query  string `json:"query"`
	Window Window `json:"window"`
	// Tool is the health row of the matched tool; its Key and Kind are empty when nothing
	// matched.
	Tool ToolHealth `json:"tool"`
	// Service is the public service the tool belongs to (rules/substitutes.yaml), when known.
	Service string `json:"service,omitempty"`
	// Sessions lists, oldest first, every session of the window that offered, called or
	// failed the tool, or that reached its service another way.
	Sessions []SessionOutcome `json:"sessions"`
	// Substitutes aggregates the other tools that reached the same service.
	Substitutes []SubstituteUse `json:"substitutes,omitempty"`
	Diagnosis   Diagnosis       `json:"diagnosis"`
	// Investigation is what an assistant on this machine should check to find the cause; nil
	// when the tool is healthy. Prompt is the same content as text, ready to paste.
	Investigation *Investigation `json:"investigation,omitempty"`
	Prompt        string         `json:"prompt,omitempty"`
	// Suggestions are names resembling the query when nothing matched.
	Suggestions []string `json:"suggestions,omitempty"`
}

// SessionOutcome is what happened to the tool in one session.
type SessionOutcome struct {
	SessionID string    `json:"session_id"`
	Project   string    `json:"project,omitempty"`
	Start     time.Time `json:"start"`
	Offered   bool      `json:"offered"`
	// ConnectFailed and Pending come from the harness's announcements (MCP servers).
	ConnectFailed bool   `json:"connect_failed,omitempty"`
	ConnectError  string `json:"connect_error,omitempty"`
	Pending       bool   `json:"pending,omitempty"`
	Calls         int    `json:"calls"`
	Success       int    `json:"success"`
	Failed        int    `json:"failed"`
	Denied        int    `json:"denied"`
	// FirstError is the first failed call's error in the session, ErrorClass its class.
	FirstError string `json:"first_error,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	// FallbackTo names the tools that took over after a failure (silent fallbacks).
	FallbackTo []string `json:"fallback_to,omitempty"`
	// SubstituteCalls counts calls that reached the tool's service another way.
	SubstituteCalls int `json:"substitute_calls,omitempty"`
}

// Outcome summarizes the session for the tool: offered / failed / denied / ok / absent.
func (s SessionOutcome) Outcome() string {
	switch {
	case s.ConnectFailed:
		return "server failed to connect"
	case s.Pending:
		return "waiting for sign-in"
	case s.Calls == 0 && s.Offered:
		return "offered, not called"
	case s.Calls == 0:
		return "absent"
	case s.Success == s.Calls:
		return fmt.Sprintf("%d ok", s.Success)
	}
	parts := []string{}
	if s.Success > 0 {
		parts = append(parts, fmt.Sprintf("%d ok", s.Success))
	}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}
	if s.Denied > 0 {
		parts = append(parts, fmt.Sprintf("%d denied", s.Denied))
	}
	return strings.Join(parts, ", ")
}

// SubstituteUse is another tool that reached the same service as the explained tool.
type SubstituteUse struct {
	Tool string `json:"tool"`
	// Example is the start of one such call's arguments (a command line or URL).
	Example  string `json:"example,omitempty"`
	Calls    int    `json:"calls"`
	Sessions int    `json:"sessions"`
	// SessionsWhileFailing / WhileHealthy / WhileAbsent split Sessions by what the explained
	// tool did in the same session: failed or was down, succeeded, or was never called.
	SessionsWhileFailing int `json:"sessions_while_failing"`
	SessionsWhileHealthy int `json:"sessions_while_healthy"`
	SessionsWhileAbsent  int `json:"sessions_while_absent"`
}

// Diagnosis is the most likely reason the tool is not used, with the evidence behind it.
type Diagnosis struct {
	// Reason is a ToolHealth Reason* value or one of the Diagnosis* values.
	Reason         string   `json:"reason"`
	Summary        string   `json:"summary"`
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
}

// Explain analyzes the sessions discovered by provs in window (and project, when set) and
// explains how the Skill or MCP server named query fared. The name is matched like health
// rows are keyed (case-insensitively, punctuation folded); an MCP server wins over a Skill of
// the same name.
func Explain(provs []*providers.LoadedProvider, window Window, project, query string, skip func(path string, err error)) (*Explanation, error) {
	want := normalizeKey(query)
	catalog := detector.DefaultSubstitutes
	var service *detector.SubstituteService
	if catalog != nil {
		service = catalog.ServiceFor(query)
	}

	b := NewBuilder(window)
	outcomes := map[string]*SessionOutcome{} // by session ID
	subs := map[string]*SubstituteUse{}
	subSessions := map[string]map[string]bool{}
	var order []string

	outcome := func(sessionID, projectName string, start time.Time) *SessionOutcome {
		if outcomes[sessionID] == nil {
			outcomes[sessionID] = &SessionOutcome{SessionID: sessionID, Project: projectName, Start: start}
			order = append(order, sessionID)
		}
		return outcomes[sessionID]
	}

	err := forEachSession(provs, window, project, skip, func(s collector.SessionInfo, projectName string, data *collector.SessionData, g *graph.Graph) {
		b.Add(s.SessionID, projectName, data.Nodes, g)
		start := sessionStart(data, s.ModTime)
		b.AddAvailability(s.SessionID, start, data.Availability)
		if !window.Contains(start) && len(data.Nodes) == 0 {
			return
		}

		if av := data.Availability; av != nil && window.Contains(start) {
			for _, name := range av.Skills {
				if normalizeKey(name) == want {
					outcome(s.SessionID, projectName, start).Offered = true
				}
			}
			for _, name := range av.MCPServers {
				if normalizeKey(name) == want {
					outcome(s.SessionID, projectName, start).Offered = true
				}
			}
			for _, f := range av.FailedServers {
				if normalizeKey(f.Name) == want {
					o := outcome(s.SessionID, projectName, start)
					o.ConnectFailed = true
					if o.ConnectError == "" {
						o.ConnectError = firstLine(f.Error)
					}
				}
			}
			for _, name := range av.PendingServers {
				if normalizeKey(name) == want {
					outcome(s.SessionID, projectName, start).Pending = true
				}
			}
		}

		for i := range data.Nodes {
			n := &data.Nodes[i]
			if analyzed := g.Nodes[n.ID]; analyzed != nil {
				n = analyzed
			}
			if n.IsInferredPlaceholder() || !window.Contains(n.Timestamp) {
				continue
			}
			if toolKeyOf(n) == want {
				o := outcome(s.SessionID, projectName, start)
				o.Offered = true
				o.Calls++
				switch n.Status {
				case models.StatusSuccess:
					o.Success++
				case models.StatusDenied:
					o.Denied++
					if o.FirstError == "" {
						o.FirstError, o.ErrorClass = firstLine(n.ErrorMessage), ErrorClassDenied
					}
				case models.StatusFailed, models.StatusTimeout:
					o.Failed++
					if o.FirstError == "" {
						o.FirstError = firstLine(n.ErrorMessage)
						if o.FirstError == "" {
							o.FirstError = firstLine(textOf(n.Output))
						}
						o.ErrorClass = classifyFailure(n)
					}
				}
				continue
			}
			// A fallback whose failed call is the explained tool
			for _, a := range n.Anomalies {
				if a.Type == models.AnomalySilentFallback && a.RelatedNodeID != "" {
					if from := g.Nodes[a.RelatedNodeID]; from != nil && toolKeyOf(from) == want {
						o := outcome(s.SessionID, projectName, start)
						o.FallbackTo = appendUnique(o.FallbackTo, n.Name)
					}
				}
			}
			// Another way to the same service: a command line that runs its CLI, or a URL on
			// its hosts. Only those arguments count; file contents that merely mention the
			// service do not.
			if service != nil && n.MCPServer == "" && !n.Auxiliary && n.Type == models.NodeTypeSystemTool {
				if _, ok := service.UsedBy(executedArguments(n.Arguments)); ok {
					o := outcome(s.SessionID, projectName, start)
					o.SubstituteCalls++
					use := subs[n.Name]
					if use == nil {
						use = &SubstituteUse{Tool: n.Name, Example: argumentExample(n.Arguments)}
						subs[n.Name] = use
						subSessions[n.Name] = map[string]bool{}
					}
					use.Calls++
					subSessions[n.Name][s.SessionID] = true
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}

	rep := b.Report()
	ex := &Explanation{Query: query, Window: window}
	if service != nil {
		ex.Service = service.Name
	}
	for _, kind := range []string{KindMCPServer, KindSkill} {
		for _, h := range rep.ToolHealth {
			if h.Kind == kind && normalizeKey(h.Key) == want {
				ex.Tool = h
				break
			}
		}
		if ex.Tool.Key != "" {
			break
		}
	}
	if ex.Tool.Key == "" {
		for _, h := range rep.ToolHealth {
			if strings.Contains(normalizeKey(h.Key), want) || strings.Contains(want, normalizeKey(h.Key)) {
				ex.Suggestions = append(ex.Suggestions, h.Key)
			}
		}
		sort.Strings(ex.Suggestions)
	}

	for _, id := range order {
		ex.Sessions = append(ex.Sessions, *outcomes[id])
	}
	sort.SliceStable(ex.Sessions, func(i, j int) bool { return ex.Sessions[i].Start.Before(ex.Sessions[j].Start) })

	for name, use := range subs {
		for sessionID := range subSessions[name] {
			use.Sessions++
			o := outcomes[sessionID]
			switch {
			case o.ConnectFailed || o.Pending || (o.Failed+o.Denied > 0 && o.Success == 0):
				use.SessionsWhileFailing++
			case o.Success > 0:
				use.SessionsWhileHealthy++
			default:
				use.SessionsWhileAbsent++
			}
		}
		ex.Substitutes = append(ex.Substitutes, *use)
	}
	sort.SliceStable(ex.Substitutes, func(i, j int) bool {
		if ex.Substitutes[i].Sessions != ex.Substitutes[j].Sessions {
			return ex.Substitutes[i].Sessions > ex.Substitutes[j].Sessions
		}
		return ex.Substitutes[i].Tool < ex.Substitutes[j].Tool
	})

	ex.Diagnosis = Diagnose(ex)
	ex.Prompt = PromptFor(ex)
	return ex, nil
}

// PromptFor returns the investigation prompt for ex, or "" when there is nothing to
// investigate (the tool is healthy). It also fills ex.Investigation.
func PromptFor(ex *Explanation) string {
	if ex.Diagnosis.Reason == DiagnosisHealthy {
		ex.Investigation = nil
		return ""
	}
	inv := Investigate(ex)
	ex.Investigation = &inv
	return inv.Render()
}

// Investigation is the structured form of the prompt: what agentlens found, the recent
// sessions, what to check on this machine, what to deliver and the rules to follow.
type Investigation struct {
	Intro          string   `json:"intro"`
	Findings       []string `json:"findings"`
	RecentSessions []string `json:"recent_sessions,omitempty"`
	Checks         []string `json:"checks"`
	Deliverable    []string `json:"deliverable"`
	Constraints    []string `json:"constraints"`
}

// Render writes the investigation as the prompt text.
func (inv Investigation) Render() string {
	var b strings.Builder
	section := func(title string, items []string, numbered bool) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n## " + title + "\n")
		for i, item := range items {
			if numbered {
				fmt.Fprintf(&b, "%d. %s\n", i+1, item)
			} else {
				b.WriteString("- " + item + "\n")
			}
		}
	}
	b.WriteString(inv.Intro + "\n")
	section("What agentlens found", inv.Findings, false)
	section("Recent sessions (oldest first)", inv.RecentSessions, false)
	section("What to check on this machine", inv.Checks, true)
	section("Deliverable", inv.Deliverable, false)
	section("Constraints", inv.Constraints, false)
	return b.String()
}

// toolKeyOf returns the normalized health key of a call: its MCP server, or the skill it
// invokes; "" for other calls.
func toolKeyOf(n *models.CallNode) string {
	switch {
	case n.MCPServer != "":
		return normalizeKey(n.MCPServer)
	case n.Type == models.NodeTypeSkill && strings.HasPrefix(n.ScopeName, "Skill: "):
		return normalizeKey(strings.TrimPrefix(n.ScopeName, "Skill: "))
	}
	return ""
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// executedArguments keeps the arguments a tool executes or fetches: a shell command line and
// a URL.
func executedArguments(args map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for _, key := range []string{"command", "url"} {
		if v, ok := args[key].(string); ok && v != "" {
			out[key] = v
		}
	}
	return out
}

// argumentExample picks the most telling argument of a substitute call: a command line or
// URL, cut short.
func argumentExample(args map[string]interface{}) string {
	for _, key := range []string{"command", "url"} {
		if s, ok := args[key].(string); ok && s != "" {
			return textutil.Truncate(firstLine(s), 80, "…")
		}
	}
	return ""
}

// Diagnose picks the most likely reason the tool is not used and lists the evidence.
func Diagnose(ex *Explanation) Diagnosis {
	h := ex.Tool
	label := h.Key
	if h.Kind == KindMCPServer {
		label = "MCP server " + h.Key
	} else if h.Kind == KindSkill {
		label = "Skill " + h.Key
	}
	var ev []string
	add := func(format string, args ...interface{}) { ev = append(ev, fmt.Sprintf(format, args...)) }

	if h.Key == "" {
		d := Diagnosis{Reason: DiagnosisNotFound,
			Summary:        fmt.Sprintf("No Skill or MCP server named %q was offered or called in this period.", ex.Query),
			Recommendation: "Check the spelling (MCP servers are matched by the name in mcp__<server>__ tool names, Skills by their listed name) or widen the period with --since."}
		if len(ex.Suggestions) > 0 {
			add("Similar names seen: %s", strings.Join(ex.Suggestions, ", "))
		}
		d.Evidence = ev
		return d
	}

	sessionsWord := func(n int) string {
		if n == 1 {
			return "1 session"
		}
		return fmt.Sprintf("%d sessions", n)
	}
	if h.SessionsOffered > 0 {
		add("Offered in %s of the period, called in %s", sessionsWord(h.SessionsOffered), sessionsWord(h.SessionsCalled))
	}
	if h.Calls > 0 {
		add("%d calls: %d succeeded, %d failed, %d denied", h.Calls, h.Calls-h.Failed-h.Denied, h.Failed, h.Denied)
	}
	if !h.LastSuccess.IsZero() {
		add("Last success %s; %s since", stamp(h.LastSuccess), sessionsWord(h.SessionsSinceLastSuccess))
	} else if h.SessionsSinceLastSuccess > 0 {
		add("No successful call in the period (%s)", sessionsWord(h.SessionsSinceLastSuccess))
	}
	if h.SessionsFailedConnect > 0 {
		add("The harness failed to connect to the server in %s; last error: %s", sessionsWord(h.SessionsFailedConnect), h.LastConnectError)
	}
	if h.SessionsPending > 0 {
		add("The server waited for a sign-in in %s", sessionsWord(h.SessionsPending))
	}
	if h.LastError != "" {
		add("Last call error (%s): %s", h.LastErrorClass, h.LastError)
	}
	if h.FallbacksFrom > 0 {
		add("%d silent fallbacks after its failures, most recently to %s", h.FallbacksFrom, h.LastFallbackTo)
	}
	var subTotal, subHealthy, subAbsent int
	for _, s := range ex.Substitutes {
		subTotal += s.Sessions
		subHealthy += s.SessionsWhileHealthy
		subAbsent += s.SessionsWhileAbsent
		add("%s reached %s instead in %s (%d while %s was failing or down, %d while it worked, %d while it was not called), e.g. %s",
			s.Tool, ex.Service, sessionsWord(s.Sessions), s.SessionsWhileFailing, h.Key, s.SessionsWhileHealthy, s.SessionsWhileAbsent, s.Example)
	}

	d := Diagnosis{Reason: h.Reason, Evidence: ev}
	switch h.Reason {
	case ReasonConnectionFailing:
		d.Summary = fmt.Sprintf("%s cannot be reached: the harness failed to connect to it in %s and it has not succeeded since.", label, sessionsWord(h.SessionsFailedConnect))
		d.Recommendation = "Fix the server connection: check its credentials, registration or endpoint, then start a new session and confirm it connects."
	case ReasonPendingSignIn:
		d.Summary = fmt.Sprintf("%s is waiting for a sign-in; the agent cannot call it until that completes.", label)
		d.Recommendation = "Complete the sign-in for this server, then confirm it connects in a new session."
	case ReasonAuthUnresolved:
		d.Summary = fmt.Sprintf("%s failed on credentials on its last call and nothing has succeeded since.", label)
		d.Recommendation = "Renew the token or re-authenticate the server, then retry a call; until then the agent keeps falling back to other tools."
	case ReasonDenied:
		d.Summary = fmt.Sprintf("%s was refused on its last call by the user or a permission rule.", label)
		d.Recommendation = "Check the permission settings that apply to this tool and whether the refusal was intended."
	case ReasonReplaced:
		d.Summary = fmt.Sprintf("%s failed and a substitute (%s) took over; it has not succeeded since.", label, h.LastFallbackTo)
		d.Recommendation = "Fix the failure of the primary tool; the substitute may give degraded or incomplete results."
	case ReasonFailing:
		d.Summary = fmt.Sprintf("%s failed on its last call (%s) and nothing has succeeded since.", label, h.LastErrorClass)
		d.Recommendation = "Reproduce the last error and fix its cause (server, arguments or environment)."
	case ReasonUnused:
		if subTotal > 0 {
			d.Reason = DiagnosisSubstitutePreferred
			d.Summary = fmt.Sprintf("%s was offered but never called, while the agent reached %s another way in %s.", label, ex.Service, sessionsWord(subTotal))
			d.Recommendation = "Make the tool the obvious choice: check its description and the instructions that steer the agent toward it, and whether a CLI is pre-authorized while the tool needs approval."
		} else {
			d.Reason = DiagnosisNoDemand
			d.Summary = fmt.Sprintf("%s was offered in %s but never called, and nothing else reached its service either.", label, sessionsWord(h.SessionsOffered))
			d.Recommendation = "Likely no task needed it in this period. Widen the period with --since, or check that its description tells the agent when to use it."
		}
	default:
		if subHealthy+subAbsent >= 2 {
			d.Reason = DiagnosisSubstitutePreferred
			d.Summary = fmt.Sprintf("%s works (last success %s), but the agent reached %s another way in %s even when the tool was available.", label, stamp(h.LastSuccess), ex.Service, sessionsWord(subHealthy+subAbsent))
			d.Recommendation = "Check what steers the agent to the substitute: instructions, a pre-authorized CLI, or a tool description that does not match the tasks."
		} else {
			d.Reason = DiagnosisHealthy
			d.Summary = fmt.Sprintf("%s works: last success %s.", label, stamp(h.LastSuccess))
			d.Recommendation = "Nothing to fix."
		}
	}
	return d
}

// BuildPrompt writes a prompt for an AI coding assistant running on this machine, asking it
// to find why the tool is not used, with agentlens's findings as the starting evidence and a
// checklist of what to inspect locally.
func BuildPrompt(ex *Explanation) string {
	return Investigate(ex).Render()
}

// Investigate builds the structured investigation for ex (see Investigation).
func Investigate(ex *Explanation) Investigation {
	h := ex.Tool
	d := ex.Diagnosis
	kind := "tool"
	switch h.Kind {
	case KindMCPServer:
		kind = "MCP server"
	case KindSkill:
		kind = "Skill"
	}
	name := h.Key
	if name == "" {
		name = ex.Query
	}
	since := "the analyzed period"
	if !ex.Window.Since.IsZero() {
		since = "since " + ex.Window.Since.UTC().Format("2006-01-02")
	}

	inv := Investigation{
		Intro: fmt.Sprintf("I am using agentlens, a local analyzer of this machine's AI agent session logs. It reports that the %s `%s` is not being used as expected. Investigate the cause on this machine and propose a fix.", kind, name),
	}
	inv.Findings = append(inv.Findings, fmt.Sprintf("Period: %s.", since), fmt.Sprintf("Diagnosis: %s. %s", ReasonTitle(d.Reason), d.Summary))
	inv.Findings = append(inv.Findings, d.Evidence...)

	start := 0
	if len(ex.Sessions) > 8 {
		start = len(ex.Sessions) - 8
	}
	for _, s := range ex.Sessions[start:] {
		line := fmt.Sprintf("%s · session %s · %s", stamp(s.Start), shortID(s.SessionID), s.Outcome())
		if s.ConnectError != "" {
			line += " · " + s.ConnectError
		} else if s.FirstError != "" {
			line += " · " + s.FirstError
		}
		if len(s.FallbackTo) > 0 {
			line += " · fell back to " + strings.Join(s.FallbackTo, ", ")
		}
		if s.SubstituteCalls > 0 {
			line += fmt.Sprintf(" · %d substitute calls", s.SubstituteCalls)
		}
		inv.RecentSessions = append(inv.RecentSessions, line)
	}

	switch h.Kind {
	case KindSkill:
		inv.Checks = skillChecks(name)
	case KindMCPServer:
		inv.Checks = mcpChecks(name)
	default:
		inv.Checks = append([]string{"The name matched nothing in the analyzed logs, so first confirm what it is: a Skill (`~/.claude/skills/`, a plugin's `skills/`, the project's `.claude/skills/`) or an MCP server (`claude mcp list`). Then follow the matching checks."},
			append(skillChecks(name), mcpChecks(name)...)...)
	}
	inv.Deliverable = []string{
		"The root cause, with the file, setting or credential that confirms it.",
		"The concrete steps to fix it (commands or edits), and what to do if the cause is outside this machine.",
		fmt.Sprintf("How to verify: start a new session, use the tool once, then run `agentlens why %s --since 1d` and confirm the diagnosis becomes \"healthy\".", name),
	}
	inv.Constraints = []string{
		"Read-only investigation first; ask before changing settings, credentials or configuration files.",
		"Never print secrets (tokens, keys, passwords); report their location and expiry only.",
	}
	return inv
}

// skillChecks lists what to inspect for a Skill.
func skillChecks(name string) []string {
	return []string{
		fmt.Sprintf("Find the skill's definition: `~/.claude/skills/%s/SKILL.md`, a plugin's `skills/` directory, or the project's `.claude/skills/`. Confirm the file exists and parses (frontmatter with `name` and `description`).", name),
		"Read its `description`: the agent chooses a skill from it. Does it say when to use the skill, with the words a user would use? Would a task in the sessions above have matched it?",
		"Check the settings that gate it: `permissions` in `~/.claude/settings.json`, `.claude/settings.json` and `.claude/settings.local.json` (deny or ask rules for `Skill`), and any hooks that block tool use.",
		"If the skill is provided by a plugin, confirm the plugin is enabled and up to date.",
		"If calls were denied, find out who denied them (the user, a permission rule, the auto mode classifier) and whether that was intended.",
	}
}

// mcpChecks lists what to inspect for an MCP server.
func mcpChecks(name string) []string {
	return []string{
		fmt.Sprintf("List the configured MCP servers and their state: run `claude mcp list` and `claude mcp get %s`. Compare with `~/.claude.json` (user and project scopes) and the project's `.mcp.json`.", name),
		"If the server fails to connect or needs a sign-in: run the server's command by hand and read its stderr; for HTTP/OAuth servers, re-authenticate (the `/mcp` command inside Claude Code lists servers and offers to sign in). Check whether a token, registration or session has expired, and whether required environment variables are set.",
		"If calls fail on credentials: find where the server reads its token (env var, keychain, config file), check its expiry and scopes, and renew it.",
		"If the agent prefers another tool: compare the permission settings (`permissions.allow` / `deny` / `ask` in the settings files) for this server's tools and for the substitute (e.g. a CLI allowed in `Bash`). Read the instructions files (`CLAUDE.md`, `AGENTS.md`, memory) for rules that steer toward or away from this server.",
		"Check the server's tool descriptions and whether the tools are deferred (loaded only through ToolSearch); a server whose tools never appear in the list cannot be called.",
	}
}

// ReasonTitle is a short title for a dormancy reason or diagnosis.
func ReasonTitle(reason string) string {
	switch reason {
	case ReasonConnectionFailing:
		return "server fails to connect"
	case ReasonPendingSignIn:
		return "waiting for sign-in"
	case ReasonAuthUnresolved:
		return "authentication failed and not fixed"
	case ReasonDenied:
		return "denied"
	case ReasonReplaced:
		return "replaced by a substitute"
	case ReasonFailing:
		return "failing"
	case ReasonUnused:
		return "offered but never called"
	case DiagnosisHealthy:
		return "healthy"
	case DiagnosisSubstitutePreferred:
		return "a substitute is preferred"
	case DiagnosisNoDemand:
		return "no task needed it"
	case DiagnosisNotFound:
		return "not found"
	}
	return reason
}

// stamp formats a time for the diagnosis and the prompt. UTC keeps the text identical on
// every machine; the CLI tables show local time.
func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
