# AgentLens: System Architecture

> **Language**: [English](ARCHITECTURE.md) | [日本語](../ja/ARCHITECTURE.md)

---

## 1. System Overview

AgentLens processes raw Generative AI execution traces into an enriched, anomaly-annotated Call Graph. It guarantees complete independence from specific AI platforms by strictly isolating vendor formats into declarative **Provider Definitions**, and ensures zero environment friction by delivering as a **single standalone Go binary with an embedded Web UI** plus a **free client-side viewer on GitHub Pages**.

```mermaid
sequenceDiagram
    autonumber
    participant AI as Generative AI Platform
    participant FS as File System / Log Storage
    participant Def as Provider Definition (YAML)
    participant Col as Data Collector (Go)
    participant GB as Graph Builder (Go)
    participant Det as Anomaly Detector (Go)
    participant CLI as Terminal CLI (Go)
    participant Web as Web UI (Embedded & GitHub Pages)

    AI->>FS: Emits trace / transcript events
    Def->>Col: Supplies parsing rules & paths
    FS->>Col: Reads raw JSONL / event stream
    Col->>GB: Emits CallNodes and ExecutionTurns (from RawTraceEvents)
    GB->>GB: Assembles CallGraph (Nodes & Edges)
    GB->>Det: Evaluates Graph structure & outputs
    Det->>GB: Annotates Graph with Anomaly Badges
    GB->>CLI: Renders terminal tree & inspection
    GB->>Web: Serves the period report and raw trace files; the page re-runs the pipeline in JS
```

---

## 2. Directory Structure & Module Breakdown

```text
agentlens/
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                 # go test / go vet / build, then the E2E suite
│   │   └── deploy-pages.yml       # Publishes web/ to GitHub Pages as is
│   ├── ISSUE_TEMPLATE/
│   └── pull_request_template.md
├── cmd/agentlens/main.go          # CLI entrypoint (standard library only)
├── docs/{en,ja}/                  # ARCHITECTURE, CONTRIBUTING, ROADMAP, SPECIFICATION
├── examples/
│   ├── providers/                 # Built-in provider definitions, embedded into the binary
│   │   ├── antigravity.yaml
│   │   ├── claude_code.yaml
│   │   ├── cursor.yaml
│   │   ├── generic_jsonl.yaml
│   │   └── embed.go
│   └── traces/                    # Sample sessions used by the docs, tests and the UI
├── internal/
│   ├── ansi/                      # Terminal colors (off when piped or NO_COLOR is set)
│   ├── cli/                       # Commands (app.go), terminal rendering, report command
│   ├── collector/                 # Session discovery, provider detection, assembler, child traces
│   ├── detector/                  # Auth, fallback and retry-loop checks; substitutes catalog
│   ├── graph/                     # Graph builder, dependencies, metrics
│   ├── models/                    # Core data models (nodes, edges, anomalies, provider config)
│   ├── providers/                 # Provider YAML loading, field expressions, scope tracking
│   ├── report/                    # Period aggregation shared by `report` and `/api/report`
│   ├── server/                    # Local HTTP API; static/index.html is the embedded UI
│   ├── textutil/                  # Truncation and formatting helpers
│   └── watcher/                   # `agentlens watch`: poll-based live tail
├── rules/substitutes.yaml         # Public services and the CLIs / hosts that replace them
├── schemas/provider_definition.schema.json
├── tests/e2e/                     # Puppeteer regression suite for the web UI
├── web/index.html                 # The web UI (single file, no build step)
├── go.mod
├── README.md
└── README.ja.md
```

---

```

---

## 3. Data Models (`internal/models`)

### 3.1 `CallNodeType`
```go
type CallNodeType string

const (
    NodeTypeUserTurn   CallNodeType = "user_turn"
    NodeTypeAgent      CallNodeType = "agent"
    NodeTypeSubagent   CallNodeType = "subagent"
    NodeTypeSkill      CallNodeType = "skill"
    NodeTypeMCPTool    CallNodeType = "mcp_tool"
    NodeTypeSystemTool CallNodeType = "system_tool"
)
```

### 3.2 `CallNode`
Represents an individual execution node within the graph:
```go
type CallNode struct {
    ID           string                 `json:"id"`
    SessionID    string                 `json:"session_id"`
    ParentID     string                 `json:"parent_id,omitempty"`
    TurnIndex    int                    `json:"turn_index"`
    CallerScope  string                 `json:"caller_scope,omitempty"` // "Agent", "Skill: <name>", "Subagent: <name>"
    ScopeName    string                 `json:"scope_name,omitempty"`   // Scope this call opens, e.g. "Skill: <name>"
    Type         CallNodeType           `json:"type"`
    Name         string                 `json:"name"`
    MCPServer    string                 `json:"mcp_server,omitempty"`
    Timestamp    time.Time              `json:"timestamp"`
    DurationMs   int64                  `json:"duration_ms,omitempty"`
    Status       CallStatus             `json:"status"` // "success", "failed", "timeout", "denied", "unknown"
    Arguments    map[string]interface{} `json:"arguments,omitempty"`
    Output       interface{}            `json:"output,omitempty"`
    ErrorMessage string                 `json:"error_message,omitempty"`
    Anomalies    []AnomalyRecord        `json:"anomalies,omitempty"`
    ParentLink   ParentLink             `json:"parent_link,omitempty"` // "sidecar", "result", "inferred"
    Auxiliary    bool                   `json:"auxiliary,omitempty"`
    Synthetic    bool                   `json:"synthetic,omitempty"`   // turn and scope nodes added by the builder
}}
```

`Status` is `denied` when the call never ran because the user or a permission policy refused it (`results.denied_regex`); it is not a failure. `Auxiliary` marks agent bookkeeping such as tool discovery, task tracking or planning (`extraction.auxiliary_tools`). Neither denied nor auxiliary calls take part in fallback and retry-loop detection: "MCP failed → tool search → shell" is still a fallback from the MCP call to the shell call.

`Synthetic` marks the turn and scope nodes the graph builder adds for structure; they are not calls and take no part in detection or call metrics.

A turn starts at a message matching `turns.filter`, labelled by the first line the user wrote (`<tag>…</tag>` blocks the harness injects are skipped; a message made only of such blocks is labelled `[tag] first line`). Messages in the user role that the user did not write are excluded with `turns.skip_regex`, matched against that label: the output of a slash command or of a `!` shell command and the marker left by an interrupted response. Their calls stay in the current turn.

A silent fallback is reported when a failed (or timed-out) MCP or Skill call is followed by a system tool (shell, HTTP fetch, file access) for any provider. The following call must have started after the failure was known: calls issued together in one model response share a timestamp and are never a fallback for each other; `anomaly_rules.known_fallbacks` adds provider-specific pairs. Each one carries a `confidence`:

- `high`: the substitute reaches the same public service — it runs that service's CLI or calls its host, as listed in the provider-independent catalog `rules/substitutes.yaml` (e.g. GitHub → `gh`) — or reuses at least two identifiers of the failed call (repository, table, IDs).
- `low`: only the order of calls suggests it. It is shown as "Possible Silent Fallback" and is a lead to check, not a finding.

The catalog only lists widely used public services. Organization-specific tools are not added to it; their fallbacks are still rated by shared identifiers.

`ParentLink` is set on calls stitched from a child (subagent) trace and records the evidence for their placement:

| Value | Evidence | Certainty |
| :--- | :--- | :--- |
| `sidecar` | The child's metadata names the spawning call (`child_traces.parent_call_id_field`). | Recorded |
| `result` | The spawning call's tool result names the child (`child_traces.result_child_id_field`), e.g. a subagent forked by a Skill. | Recorded |
| `inferred` | No record links the child to a call. Its calls are placed under a placeholder subagent node (`id: inferred-subagent-<child>`, `parent_id` empty) in the turn active when the child started, and the dependency row is flagged `inferred: true`. | Best guess |

### 3.3 `CallEdge`
```go
type CallEdgeType string

const (
    EdgeTypeCalls      CallEdgeType = "calls"
    EdgeTypeSpawns     CallEdgeType = "spawns"
    EdgeTypeFallbackTo CallEdgeType = "fallback_to"
    EdgeTypeRetries    CallEdgeType = "retries"
)

type CallEdge struct {
    SourceID string       `json:"source_id"`
    TargetID string       `json:"target_id"`
    Type     CallEdgeType `json:"type"`
}
```

### 3.4 `AnomalyRecord`
```go
type AnomalyType string

const (
    AnomalySilentFallback AnomalyType = "silent_fallback"
    AnomalyAuthExpired    AnomalyType = "auth_expired"
    AnomalyRetryLoop      AnomalyType = "retry_loop"
)

type AnomalyRecord struct {
    ID             string      `json:"id"`
    Type           AnomalyType `json:"type"`
    Severity       string      `json:"severity"` // "info", "warning", "critical"
    NodeID         string      `json:"node_id"`
    RelatedNodeID  string      `json:"related_node_id,omitempty"`
    Title          string      `json:"title"`
    Description    string      `json:"description"`
    Recommendation string      `json:"recommendation,omitempty"`
    Confidence     Confidence  `json:"confidence,omitempty"` // silent fallbacks: "high" or "low"
    Evidence       string      `json:"evidence,omitempty"`   // what the confidence rests on
}}
```

---

## 4. Web UI Dual-Hosting Architecture

AgentLens's Web UI is one hand-written page, `web/index.html`, with no build step:
1. **GitHub Pages Deployment**:
   - `.github/workflows/deploy-pages.yml` publishes `web/` as is to `https://tsbkw.github.io/agentlens`.
   - 100% private: users can drag and drop trace files directly onto the browser window. Log parsing and graph rendering execute client-side via JavaScript without transmitting data to any server.
2. **Local Embedded Server**:
   - `internal/server/static/index.html` is a copy of `web/index.html`, embedded with `//go:embed static/index.html`. `TestEmbeddedUIMatchesPagesUI` fails when the two differ, so a UI change is copied over before committing.
   - Running `agentlens ui` spins up a local HTTP server on `127.0.0.1:8000`, which also serves the period report and raw session files to the page's local sessions dashboard.
