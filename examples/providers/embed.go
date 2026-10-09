// Package providerdefs embeds the provider definitions shipped with AgentLens,
// so the binary works without the YAML files on disk.
package providerdefs

import "embed"

// FS holds the built-in provider definition YAML files.
//
//go:embed *.yaml
var FS embed.FS

// Order lists the built-in definitions in precedence order; the first is the default.
var Order = []string{
	"antigravity.yaml",
	"claude_code.yaml",
	"cursor.yaml",
	"generic_jsonl.yaml",
}
