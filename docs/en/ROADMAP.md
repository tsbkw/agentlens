# AgentLens: Development Roadmap & Progress Tracking

> **Language**: [English](ROADMAP.md) | [日本語](../ja/ROADMAP.md)

This document tracks all features, phases, and pull requests for AgentLens. In AI-driven development, this file serves as the single source of truth for task progress. When opening and merging PRs, update the checkboxes and reference the merged PR numbers.

---

## Progress Overview

| Phase | Description | Status | Target / PR |
| :--- | :--- | :--- | :--- |
| **Phase 0** | Project Foundation, Specs, Go Architecture, AI Agent Guidance & PR Setup | Completed | PR #1 |
| **Phase 1** | Core Data Models, Provider Definition Schema & YAML Loader | Completed | PR #2 |
| **Phase 2** | Trace Collector Engine & Call Graph Assembler (DAG in Go) | Completed | PR #3 |
| **Phase 3** | Anomaly & Silent Fallback Detection Engine | Completed | PR #4 |
| **Phase 4** | CLI Visualization (`agentlens graph`, `inspect`, `watch`) | Completed | PR #5 |
| **Phase 5** | Web UI Dashboard (Embedded UI & Free GitHub Pages Live Viewer) | Completed | PR #6 |
| **Phase 6** | Call Graph Redesign: Caller ➔ Callee Graph & Turn Trace Engine | Completed | PR #7 |
| **Phase 7** | Multi-Provider Adapters & Live Tail Watcher (`agentlens watch`) | Completed | PR #8 |
| **Phase 8** | Internationalization (i18n: en & ja) CLI/UI Localization | Planned | PR #9 |
| **Phase 9** | Claude Code Support (real `~/.claude/projects` transcripts end-to-end) | In Progress | PR #12– |

---

## Detailed Task Breakdown

### Phase 0: Project Foundation & Specifications (PR #1)
- [x] Create project repository structure, `.gitignore`, and `go.mod`
- [x] Author bilingual Functional Specification (`docs/en/SPECIFICATION.md`, `docs/ja/SPECIFICATION.md`)
- [x] Author bilingual Architecture Design (`docs/en/ARCHITECTURE.md`, `docs/ja/ARCHITECTURE.md`)
- [x] Define Provider Definition JSON Schema (`schemas/provider_definition.schema.json`)
- [x] Author example provider configs (`examples/providers/antigravity.yaml`, `generic_jsonl.yaml`)
- [x] Setup GitHub templates (`.github/pull_request_template.md`, issue templates)
- [x] Add AI Agent instructions (`AGENTS.md`, `.github/copilot-instructions.md`, `.cursorrules`)
- [x] Configure GitHub Actions CI (`.github/workflows/ci.yml`) and Pages deployment (`deploy-pages.yml`)
- [x] Set GitHub branch protection (Maintainer can merge anytime, others require review approval)
- [x] Implement core Go models (`internal/models/`) and CLI entrypoint (`cmd/agentlens/`)
- [x] Establish PR-based development workflow and merge initial PR

### Phase 1: Core Models & Provider Loader (PR #2)
- [x] Implement Provider Definition Loader (`internal/providers/loader.go`, YAML validator)
- [x] Implement Session & Trace Data Parsers (`internal/providers/parser.go`)
- [x] Add unit tests for YAML schema validation and provider parsing

### Phase 2: Collector Engine & Call Graph Assembler (PR #3)
- [x] Implement `BaseCollector` and file-based JSONL log stream reader (`internal/collector/`)
- [x] Implement `GraphBuilder` reconstructing DAG in Go (`internal/graph/`)
- [x] Implement subagent spawning and turn hierarchy stitching
- [x] Add unit tests with synthetic transcript traces

### Phase 3: Anomaly & Fallback Detection Engine (PR #4)
- [x] Implement `AuthExpirationChecker` (regex pattern matching for token/401/403)
- [x] Implement `SilentFallbackDetector` (transition detection from failed tools to shell/substitute tools)
- [x] Implement `RetryLoopDetector` (consecutive failure detection)
- [x] Add unit tests for anomaly detection rules and edge tagging (`FALLBACK_TO`)

### Phase 4: CLI Visualization (PR #5)
- [x] Implement `agentlens list` (list discovered sessions and trace files)
- [x] Implement `agentlens graph <session_id>` with tree visualization
- [x] Implement `agentlens inspect <call-id>` for node details, arguments, and anomaly reports
- [x] Embed default Antigravity / Gemini provider configuration into CLI binary

### Phase 5: Web UI Dashboard (PR #6)
- [x] Implement local HTTP server in Go (`agentlens ui`) with embedded web assets
- [x] Implement interactive DAG graph visualization and statistics in `web/index.html`
- [x] Implement anomaly alert banner and inspector drawer in Web UI
- [x] Enable 100% private client-side drag-and-drop trace viewing on GitHub Pages

### Phase 6: Call Graph Redesign: Caller ➔ Callee Graph & Turn Trace Engine (PR #7)
- [x] Redesign Call Graph from 160-level linear staircase to true **Caller ➔ Callee Dependency Graph**
- [x] Detect Skill activations (`view_file` on `.../skills/<name>/SKILL.md`, `skill_*`) and Subagents
- [x] Implement Turn-by-Turn execution trace engine with user prompt sanitization
- [x] Add `agentlens trace <session-id>` CLI command alongside `agentlens graph <session-id>`
- [x] Update Web UI with dual tabs: **Caller ➔ Callee Graph** and **Turn Execution Trace**

### Phase 7: Multi-Provider Adapters & Live Log Watcher (PR #8)
- [x] Implement live log watcher (`agentlens watch`) with automatic file tailing
- [x] Claude Code declarative provider definition (`examples/providers/claude_code.yaml`)
- [x] Cursor Composer declarative provider definition (`examples/providers/cursor.yaml`)
- [x] Unit tests for live stream watching and multi-provider YAML validation

### Phase 8: Full i18n & Polishing (PR #9)
- [ ] Localization manager supporting English and Japanese
- [ ] Localized CLI strings and UI strings
- [ ] End-to-end integration tests and documentation finalization

### Phase 9: Claude Code Support (PR #12–)
- [x] Declarative tool call extraction from nested content blocks (`tool_calls_path`, `tool_call_item_filter`), `||` filters, and `mcp_server_regex` (PR #12)
- [x] Declarative turn detection and ID-based tool result correlation (`tool_use` ↔ `tool_result`) (PR #13)
- [x] Declarative Skill / Subagent scope rules (remove hard-coded Antigravity tool names from core layers) (PR #14)
- [x] Shared incremental session assembler so `agentlens watch` follows the same declarative turn / result / scope rules (PR #15)
- [x] CLI / Web server / watcher `--provider` selection with a built-in Claude Code definition and trace-format auto-detection (PR #16)
- [ ] Stitch Claude Code subagent transcripts (`<session>/subagents/agent-*.jsonl`) into the call graph
- [ ] Claude Code trace support in the browser viewer (GitHub Pages) and docs
