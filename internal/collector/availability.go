package collector

import (
	"fmt"
	"time"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

// availabilityTracker accumulates what the harness announced the agent could call in a
// session (extraction.availability): skills, tools, MCP servers that failed to connect and
// servers waiting for the user to sign in.
type availabilityTracker struct {
	cfg      *models.AvailabilityConfig
	provider *providers.LoadedProvider
	fields   models.FieldMappings

	skills   []string
	tools    []string
	servers  []string
	serverAt map[string]time.Time
	failed   []models.ServerFailure
	pending  []string
	seen     map[string]bool
	failedAt map[string]int // server name -> index in failed
}

func newAvailabilityTracker(provider *providers.LoadedProvider) *availabilityTracker {
	return &availabilityTracker{
		cfg:      provider.Definition.Extraction.Availability,
		provider: provider,
		fields:   provider.Definition.Extraction.Fields,
		seen:     make(map[string]bool),
		failedAt: make(map[string]int),
		serverAt: make(map[string]time.Time),
	}
}

// Feed records what payload announces, if anything.
func (t *availabilityTracker) Feed(payload map[string]interface{}, fallbackTime time.Time) {
	if t.cfg == nil {
		return
	}
	at := fallbackTime
	if t.fields.Timestamp != "" {
		if parsed, ok := providers.ParseTimestampValue(providers.ResolveField(payload, t.fields.Timestamp)); ok {
			at = parsed
		}
	}
	if rule := t.cfg.Skills; rule != nil && matches(rule.Filter, payload) {
		for _, name := range stringList(providers.ResolveField(payload, rule.NamesField)) {
			t.add("skill", &t.skills, name)
		}
	}
	if rule := t.cfg.Tools; rule != nil && matches(rule.Filter, payload) {
		for _, name := range stringList(providers.ResolveField(payload, rule.AddedField)) {
			t.add("tool", &t.tools, name)
			if server := t.serverOf(name); server != "" {
				t.add("server", &t.servers, server)
				if !at.IsZero() && at.After(t.serverAt[server]) {
					t.serverAt[server] = at
				}
			}
		}
	}
	if rule := t.cfg.FailedServers; rule != nil && matches(rule.Filter, payload) {
		ts := at
		for _, item := range listItems(providers.ResolveField(payload, rule.ItemsField)) {
			name, errText := itemNameAndError(item, rule)
			if name == "" {
				continue
			}
			if idx, ok := t.failedAt[name]; ok {
				t.failed[idx].Occurrences++
				continue
			}
			t.failedAt[name] = len(t.failed)
			t.failed = append(t.failed, models.ServerFailure{Name: name, Error: errText, Timestamp: ts, Occurrences: 1})
		}
	}
	if rule := t.cfg.PendingServers; rule != nil && matches(rule.Filter, payload) {
		for _, item := range listItems(providers.ResolveField(payload, rule.ItemsField)) {
			if name, _ := itemNameAndError(item, rule); name != "" {
				t.add("pending", &t.pending, name)
			}
		}
	}
}

// Result returns the accumulated availability, or nil when nothing was announced.
func (t *availabilityTracker) Result() *models.Availability {
	if len(t.skills) == 0 && len(t.tools) == 0 && len(t.failed) == 0 && len(t.pending) == 0 {
		return nil
	}
	av := &models.Availability{
		Skills:         t.skills,
		Tools:          t.tools,
		MCPServers:     t.servers,
		FailedServers:  t.failed,
		PendingServers: t.pending,
	}
	if len(t.serverAt) > 0 {
		av.ServerAnnouncedAt = t.serverAt
	}
	return av
}

func (t *availabilityTracker) add(kind string, list *[]string, name string) {
	key := kind + "\x00" + name
	if name == "" || t.seen[key] {
		return
	}
	t.seen[key] = true
	*list = append(*list, name)
}

// serverOf extracts the MCP server from a tool name with fields.mcp_server_regex.
func (t *availabilityTracker) serverOf(toolName string) string {
	re := t.provider.MCPServerRegex
	if re == nil {
		return ""
	}
	m := re.FindStringSubmatch(toolName)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func matches(filter string, payload map[string]interface{}) bool {
	return filter == "" || providers.MatchesFilter(payload, filter)
}

func listItems(v interface{}) []interface{} {
	items, _ := v.([]interface{})
	return items
}

// itemNameAndError reads a server entry that is either a plain name or an object with the
// rule's name and error fields.
func itemNameAndError(item interface{}, rule *models.ServerListRule) (string, string) {
	switch v := item.(type) {
	case string:
		return v, ""
	case map[string]interface{}:
		name, _ := providers.ResolveField(v, rule.NameField).(string)
		var errText string
		if rule.ErrorField != "" {
			switch e := providers.ResolveField(v, rule.ErrorField).(type) {
			case string:
				errText = e
			case nil:
			default:
				errText = fmt.Sprint(e)
			}
		}
		return name, errText
	}
	return "", ""
}

// stringList returns the string elements of a JSON array.
func stringList(v interface{}) []string {
	items, _ := v.([]interface{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
