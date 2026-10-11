package detector

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/rules"
)

// SubstituteService is a public service and the CLIs and hosts that can stand in for its MCP
// server (rules/substitutes.yaml).
type SubstituteService struct {
	Name       string   `yaml:"name" json:"name"`
	MCPServers []string `yaml:"mcp_servers" json:"mcp_servers"`
	Commands   []string `yaml:"commands" json:"commands"`
	Hosts      []string `yaml:"hosts" json:"hosts"`
}

// SubstituteCatalog is the provider-independent list of SubstituteServices.
type SubstituteCatalog struct {
	Services []SubstituteService `yaml:"services" json:"services"`
}

// DefaultSubstitutes is the catalog shipped with AgentLens.
var DefaultSubstitutes = mustLoadSubstitutes(rules.Substitutes)

// LoadSubstituteCatalog parses a substitutes catalog.
func LoadSubstituteCatalog(data []byte) (*SubstituteCatalog, error) {
	var c SubstituteCatalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("invalid substitutes catalog: %w", err)
	}
	return &c, nil
}

func mustLoadSubstitutes(data []byte) *SubstituteCatalog {
	c, err := LoadSubstituteCatalog(data)
	if err != nil {
		panic(err)
	}
	return c
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// ServiceFor returns the service whose name appears as a word of the MCP server name.
func (c *SubstituteCatalog) ServiceFor(mcpServer string) *SubstituteService {
	words := make(map[string]bool)
	for _, w := range nonWord.Split(strings.ToLower(mcpServer), -1) {
		words[w] = true
	}
	for i := range c.Services {
		for _, s := range c.Services[i].MCPServers {
			if words[strings.ToLower(s)] {
				return &c.Services[i]
			}
		}
	}
	return nil
}

// UsedBy reports how a call with these arguments reaches the service: through one of its
// CLIs run as a command, or one of its hosts.
func (s *SubstituteService) UsedBy(args map[string]interface{}) (string, bool) {
	var texts []string
	collectStrings(args, &texts)
	for _, text := range texts {
		lower := strings.ToLower(text)
		for _, cmd := range s.Commands {
			if runsCommand(lower, cmd) {
				return fmt.Sprintf("runs %s, the %s CLI", cmd, s.Name), true
			}
		}
		for _, host := range s.Hosts {
			if strings.Contains(lower, strings.ToLower(host)) {
				return fmt.Sprintf("calls %s (%s)", host, s.Name), true
			}
		}
	}
	return "", false
}

// runsCommand reports whether cmd appears in command position of a shell command line.
func runsCommand(line, cmd string) bool {
	re := regexp.MustCompile(`(^|[\s;&|(` + "`" + `$])` + regexp.QuoteMeta(strings.ToLower(cmd)) + `(\s|$)`)
	return re.MatchString(line)
}

var identifierStopwords = map[string]bool{
	"true": true, "false": true, "null": true, "none": true, "json": true, "text": true,
	"http": true, "https": true, "with": true, "from": true, "this": true, "that": true,
	"page": true, "file": true, "list": true, "name": true, "type": true, "user": true,
	"users": true, "data": true, "query": true, "select": true, "where": true, "limit": true,
	"order": true, "count": true, "status": true, "error": true, "result": true, "value": true,
	"echo": true, "head": true, "tail": true, "grep": true, "find": true, "home": true,
}

var identifierToken = regexp.MustCompile(`[a-z0-9][a-z0-9_.\-]*[a-z0-9]`)

// dateLike matches dates and times, which calls share by coincidence (e.g. "2026-08-26").
var dateLike = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}|^\d{1,2}:\d{2}`)

// sharedIdentifiers lists distinctive values (names, IDs, paths segments) that appear in the
// arguments of both calls, e.g. the same repository and issue number.
func sharedIdentifiers(a, b map[string]interface{}) []string {
	tokens := func(args map[string]interface{}) map[string]bool {
		var texts []string
		collectStrings(args, &texts)
		set := make(map[string]bool)
		for _, text := range texts {
			for _, token := range identifierToken.FindAllString(strings.ToLower(text), -1) {
				// "dataset.table" or "file.ext" also counts by its parts
				for _, t := range append([]string{token}, strings.Split(token, ".")...) {
					if identifierStopwords[t] || dateLike.MatchString(t) {
						continue
					}
					hasLetter := strings.IndexFunc(t, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
					if (hasLetter && len(t) >= 4) || (!hasLetter && len(t) >= 3) {
						set[t] = true
					}
				}
			}
		}
		return set
	}
	tb := tokens(b)
	var shared []string
	for t := range tokens(a) {
		if tb[t] {
			shared = append(shared, t)
		}
	}
	sort.Strings(shared)
	return shared
}

// collectStrings gathers the string and number values of nested arguments (not their keys).
func collectStrings(v interface{}, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case float64:
		// Plain digits, so a numeric ID matches the same ID written as text
		*out = append(*out, strconv.FormatFloat(x, 'f', -1, 64))
	case int, int64:
		*out = append(*out, fmt.Sprint(x))
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectStrings(x[k], out)
		}
	case []interface{}:
		for _, e := range x {
			collectStrings(e, out)
		}
	}
}

// fallbackEvidence rates how likely target substitutes the failed source call: high when the
// target reaches the same public service or reuses at least two of its identifiers, low when
// only the "failed MCP/Skill call followed by a system tool" pattern holds.
func fallbackEvidence(catalog *SubstituteCatalog, source, target *models.CallNode) (models.Confidence, string) {
	if catalog != nil && source.MCPServer != "" {
		if svc := catalog.ServiceFor(source.MCPServer); svc != nil {
			if how, ok := svc.UsedBy(target.Arguments); ok {
				return models.ConfidenceHigh, fmt.Sprintf("%s %s", target.Name, how)
			}
		}
	}
	if shared := sharedIdentifiers(source.Arguments, target.Arguments); len(shared) >= 2 {
		if len(shared) > 4 {
			shared = shared[:4]
		}
		return models.ConfidenceHigh, fmt.Sprintf("both calls use %s", strings.Join(shared, ", "))
	}
	return models.ConfidenceLow, "only the order of calls suggests it: no shared service, CLI or identifier"
}
