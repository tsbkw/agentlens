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
| **Phase 4** | CLI Visualization (`agentlens graph`, `inspect`, `watch`) | Planned | PR #5 |
| **Phase 5** | Web UI Dashboard (Embedded UI & Free GitHub Pages Live Viewer) | Planned | PR #6 |
| **Phase 6** | Built-in Provider Adapters (Antigravity, Claude Code, Cursor, etc.) | Planned | PR #7 |
| **Phase 7** | Internationalization (i18n: en & ja) CLI/UI Localization | Planned | PR #8 |

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
- [ ] Implement `agentlens list` (list discovered sessions and trace files)
- [ ] Implement `agentlens graph <session_id>` with tree visualization
- [ ] Implement `agentlens inspect <call-id>` for node details, arguments, and anomaly reports
- [ ] Implement `agentlens watch` for live tailing of running AI sessions

### Phase 5: Web UI Dashboard (PR #6)
- [ ] Implement local HTTP server in Go (`agentlens ui`) with embedded web assets
- [ ] Implement interactive DAG graph visualization in `web/`
- [ ] Implement timeline / waterfall view for parallel tool execution
- [ ] Implement anomaly alert banner and inspector drawer
- [ ] Enable client-side drag-and-drop trace viewing on GitHub Pages

### Phase 6: Multi-Provider Adapters (PR #7)
- [ ] Antigravity transcript parser and live log watcher
- [ ] Claude Code session trace adapter
- [ ] Cursor / Roo Code trace adapter
- [ ] Documentation and user guide for creating custom provider definitions

### Phase 7: Full i18n & Polishing (PR #8)
- [ ] Localization manager supporting English and Japanese
- [ ] Localized CLI strings and UI strings
- [ ] End-to-end integration tests and documentation finalization
