// Package rules embeds provider-independent detection knowledge shipped with AgentLens.
package rules

import _ "embed"

// Substitutes is the catalog of public services and the CLIs and hosts that can stand in
// for their MCP servers (substitutes.yaml).
//
//go:embed substitutes.yaml
var Substitutes []byte
