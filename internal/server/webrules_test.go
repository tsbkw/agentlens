package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsbkw/agentlens/internal/models"
	"github.com/tsbkw/agentlens/internal/providers"
)

const (
	rulesOpenTag  = `<script type="application/json" id="provider-rules">`
	rulesCloseTag = `</script>`
)

// webProviderRule is the subset of a provider definition the browser viewer needs.
type webProviderRule struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Extraction   models.ExtractionConfig   `json:"extraction"`
	AnomalyRules models.AnomalyRulesConfig `json:"anomaly_rules"`
}

var webPages = []string{
	filepath.Join("..", "..", "web", "index.html"),
	filepath.Join("static", "index.html"),
}

// TestWebProviderRules keeps the provider rules embedded in the browser viewer in sync with
// examples/providers/*.yaml. Run with UPDATE_WEB_RULES=1 to regenerate them.
func TestWebProviderRules(t *testing.T) {
	builtins, err := providers.LoadBuiltinProviders()
	if err != nil {
		t.Fatalf("LoadBuiltinProviders failed: %v", err)
	}
	rules := make([]webProviderRule, 0, len(builtins))
	for _, p := range builtins {
		rules = append(rules, webProviderRule{
			ID:           p.Definition.Provider.ID,
			Name:         p.Definition.Provider.Name,
			Extraction:   p.Definition.Extraction,
			AnomalyRules: p.Definition.AnomalyRules,
		})
	}
	// json.Marshal escapes <, > and &, so the payload cannot close the script element
	want, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("Failed to marshal rules: %v", err)
	}

	for _, page := range webPages {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("Failed to read %s: %v", page, err)
		}
		html := string(raw)
		start := strings.Index(html, rulesOpenTag)
		if start < 0 {
			t.Fatalf("%s: missing %s block", page, rulesOpenTag)
		}
		start += len(rulesOpenTag)
		end := strings.Index(html[start:], rulesCloseTag)
		if end < 0 {
			t.Fatalf("%s: unterminated provider-rules block", page)
		}
		end += start

		if os.Getenv("UPDATE_WEB_RULES") == "1" {
			updated := html[:start] + string(want) + html[end:]
			if err := os.WriteFile(page, []byte(updated), 0o644); err != nil {
				t.Fatalf("Failed to update %s: %v", page, err)
			}
			continue
		}
		if html[start:end] != string(want) {
			t.Errorf("%s: embedded provider rules are out of date; run `UPDATE_WEB_RULES=1 go test ./internal/server/ -run TestWebProviderRules`", page)
		}
	}
}

func TestEmbeddedUIMatchesPagesUI(t *testing.T) {
	pages, err := os.ReadFile(webPages[0])
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile(webPages[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(pages) != string(embedded) {
		t.Errorf("web/index.html and internal/server/static/index.html differ; copy web/index.html over the embedded UI")
	}
}
