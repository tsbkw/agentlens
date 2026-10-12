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
| **Phase 9** | Claude Code Support (real `~/.claude/projects` transcripts end-to-end) | Completed | PR #12–#19 |
| **Phase 10** | Claude Code Follow-ups (deterministic output, live and in-browser subagent stitching) | Completed | PR #20–#33 |
| **Phase 11** | Review Follow-ups (docs match the code, CLI output, detection fixes, robustness, UI polish) | In progress | PR #35– |
| **Phase 12** | Dormant Tools (availability events, tools that stopped being called, `why` analysis with a diagnostic prompt) | Done | PR #53–#56 |
| **Phase 13** | Deeper Analysis (proxy routing, latency spikes, canvas and timeline views) | Planned | — |

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
- [x] Implement the call graph tables and statistics in `web/index.html`
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

### Phase 9: Claude Code Support (PR #12–#19)
- [x] Declarative tool call extraction from nested content blocks (`tool_calls_path`, `tool_call_item_filter`), `||` filters, and `mcp_server_regex` (PR #12)
- [x] Declarative turn detection and ID-based tool result correlation (`tool_use` ↔ `tool_result`) (PR #13)
- [x] Declarative Skill / Subagent scope rules (remove hard-coded Antigravity tool names from core layers) (PR #14)
- [x] Shared incremental session assembler so `agentlens watch` follows the same declarative turn / result / scope rules (PR #15)
- [x] CLI / Web server / watcher `--provider` selection with a built-in Claude Code definition and trace-format auto-detection (PR #16)
- [x] Reduce anomaly false positives on real sessions: same-tool retries, unconfirmed fallbacks, auth patterns in successful system tool output (PR #17)
- [x] Stitch Claude Code subagent transcripts (`<session>/subagents/agent-*.jsonl`) into the call graph (PR #18)
- [x] Claude Code trace support in the browser viewer (GitHub Pages) and docs (PR #19)

### Phase 10: Claude Code Follow-ups (PR #20–#33)
- [x] Deterministic graph, dependency tree and anomaly ordering across runs (PR #20)
- [x] `agentlens watch` follows subagent transcripts as they are written (PR #21)
- [x] Subagent stitching in the browser viewer (multi-file / folder input) (PR #22)
- [x] Truncate Japanese and other multi-byte text on character boundaries (PR #23)
- [x] Link subagents forked by a Skill through the tool result, mark unlinkable ones as `inferred`, and drop calls a forked subagent replays from its parent (PR #24)
- [x] `agentlens watch` re-links an inferred subagent once a late tool result names its spawning call (PR #25)
- [x] Check only failed calls for auth errors and report each retry streak once (PR #27)
- [x] Treat calls refused by the user or a permission policy as `denied` (not failed) and skip bookkeeping tools (`auxiliary_tools`) in fallback and retry sequences (PR #28)
- [x] Rate silent fallbacks `high` / `low` confidence from a shared catalog of public services (`rules/substitutes.yaml`) and shared identifiers (PR #29)
- [x] `agentlens report`: aggregate calls and anomalies of all sessions over a period (PR #30)
- [x] `agentlens ui` listens on loopback only and rejects foreign `Host` headers (PR #31)
- [x] Local sessions dashboard in `agentlens ui`: period and project filters, report counts, click through to a session's call graph (PR #32, #33)

### Phase 11: Review Follow-ups (PR #35–)
- [x] Plain CLI output when piped (`NO_COLOR`, TTY detection), grouped alerts that quote the error, durations in `trace` / `inspect` and the web viewer (PR #35)
- [x] Go PR template and `.gitignore`, `LICENSE` file, `go 1.23` directive, relative doc links (PR #36)
- [x] Docs describe what is implemented; `GraphMetrics` fills depth and tool time; unused anomaly types removed (PR #37)
- [x] No fallback between calls issued together; retry loops inside Skill scopes; calls before the first prompt get turn 0; `trace` renders each call once; web viewer parity (PR #38)
- [x] Auth failure pattern requires context around 401/403 (PR #39)
- [x] Collector survives over-long and malformed lines and a BOM; epoch and date-time timestamps; duplicate call IDs kept apart; turn labels skip injected tag blocks; symlink-safe discovery; ambiguous session prefixes rejected; YAML validation of globs, filters and correlation (PR #40)
- [x] `ui --port`, bind before announcing, `watch` reopens truncated files and scans idle sessions cheaply, `inspect <call-id> [session-id]` (PR #41)
- [x] Web viewer: phone layout, filters in the trace view, grouped alerts, denied stat, clean state per session, unknown files reported (PR #42)
- [x] A user's next prompt is no longer taken as the previous step's tool result (PR #43)
- [x] `agentlens ui` stops and replaces an agentlens ui already running on its port; other programs are left alone (PR #45)
- [x] gofmt applied repo-wide and enforced in CI; `Makefile` with `make check`; git hooks for commit (gofmt, vet, build) and push (Go tests) via `make hooks` (PR #50)
- [x] `turns.skip_regex`: a local command's output and interruption markers are not turns, so a slash command keeps its calls (PR #51)
- [x] Synthetic Claude Code regression fixture (37 calls, nested and forked subagents, parallel calls, retry loop, fallbacks, denials, compaction, truncated line) with a golden file checked by the Go pipeline and the browser engine (PR #52)

### Phase 12: Dormant Tools (PR #53–#56)
- [x] `extraction.availability`: skills, tools, failed and pending MCP servers announced by the harness are collected per session (PR #53)
- [x] Report section listing Skills and MCP servers that stopped working, with the reason (connection failing, waiting for sign-in, auth unresolved, denied, replaced, failing) and the ones offered but never called (PR #54)
- [x] Dashboard: dormant tools table, Why? buttons, analysis panel with a copyable Claude Code prompt (PR #56)
- [x] `agentlens why <tool>` and `GET /api/why`: per-session outcomes, substitutes that reached the same service, diagnosis with evidence, and a Claude Code prompt to investigate on the machine (PR #55)

### Phase 13: Deeper Analysis (Planned)
- [ ] Indirect proxy routing detection (tool A reached through tool B)
- [ ] Latency spike detection against per-tool baselines
- [ ] Node-edge canvas and timeline (waterfall) views in the web UI
- [ ] Apply a custom `--provider` YAML in the browser's analysis
