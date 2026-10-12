# Synthetic regression fixtures

Hand-made trace files that exercise the whole pipeline (collector → graph → detector → report)
and the browser engine in `web/index.html`. Every name, path, prompt, output and error text in
them is invented; only the *shapes* of the events follow the real transcript formats.

| Fixture | Format | Golden | Go test | Browser test |
| :--- | :--- | :--- | :--- | :--- |
| `gemini_edge_cases.jsonl` | Antigravity | `gemini_edge_cases_golden.json` | `tests/integration/synthetic_pipeline_test.go` | `tests/e2e/synthetic_parity_test.mjs` |
| `claude_code_regression.jsonl` + `claude_code_regression/` | Claude Code | `claude_code_regression_golden.json` | `tests/integration/claude_code_regression_test.go` | `tests/e2e/claude_code_parity_test.mjs` |

## Claude Code regression fixture

One session of 37 calls over 4 turns, with three subagent transcripts in
`claude_code_regression/subagents/` and a stray `tool-results/` file that must be ignored.
Each part guards a behaviour that broke or nearly broke once:

| Where | Shape | Expected behaviour |
| :--- | :--- | :--- |
| preamble | `permission-mode`, `attachment`, `file-history-snapshot`, `queue-operation` events | not calls, not turns |
| turn 1 | `<command-name>/pr-review` followed by `<local-command-stdout>` | one turn labelled `/pr-review`; the output message is not a turn (`turns.skip_regex`) |
| turn 1 | `Skill` call, then an `isMeta` message | scope `Skill: pr-review`; the meta message is not a turn |
| turn 1 | three calls in one response as separate events sharing `message.id` | the `Read` that finished before the GitHub failures is not a fallback |
| turn 1 | two GitHub MCP failures (`Bad credentials`, `HTTP 401`) then `gh pr view` | two auth anomalies; one high-confidence fallback to the first `Bash`; the second `gh` call is not flagged |
| turn 1 | `Agent` call with a sidecar naming it, nested `Agent` inside | calls nested two levels deep, `parent_link: sidecar`, depth 4 |
| turn 1 | `Bash` fails then `Bash` succeeds inside the subagent | same tool: no fallback |
| turn 1 | `<task-notification>` message, `AskUserQuestion` | not a turn; the question is auxiliary with a 2m15s duration |
| turn 2 | prompt preceded by a `<system-reminder>` block, Japanese, two lines | label is the first line the user wrote |
| turn 2 | `thinking` block, `ToolSearch` whose result is a `tool_reference` block | ignored / auxiliary with empty output |
| turn 2 | MCP result persisted to a `tool-results/*.txt` file | ordinary text output; the file is not a trace |
| turn 2 | `mcp__postgres__query` fails three times, then `psql` fails, then `docker compose` | retry loop on the third call; high-confidence fallback to `psql`; `compose` is not a second fallback |
| turn 2 | recovered MCP result containing `4013` and `last_error` | not an auth failure |
| turn 2 | user rejection and auto-mode refusal | status `denied`, not `failed`; counted separately |
| turn 2 | `WebFetch` returns `status code 403`, then `curl` | auth anomaly on a system tool; `curl` is not a fallback (the source is not an MCP tool or Skill) |
| turn 3 | `compact_boundary` system event and `isCompactSummary` message | not turns |
| turn 3 | `Skill` whose result carries `agentId` of a forked transcript that replays the call | the fork's calls link with `parent_link: result`; the replayed `Skill` call is counted once |
| turn 3 | `[Request interrupted by user]` | not a turn |
| turn 4 | `Command timed out after 2m` then the same command succeeds | failed with a 120s duration; no fallback |
| turn 4 | `isApiErrorMessage` assistant event | not a call |
| end | truncated last line | `skipped_lines: 1`, everything before it is kept |

### Updating the golden file

After an intended behaviour change, regenerate and review the diff:

```bash
UPDATE_GOLDEN=1 go test ./tests/integration -run TestClaudeCodeRegressionFixture
git diff tests/fixtures/synthetic/claude_code_regression_golden.json
```

The browser parity test reads the same golden file, so run `make e2e` afterwards.

### Editing the fixture

Keep it synthetic: public service names only (`github`, `slack`, `postgres`), invented paths
under `/home/dev/workspace/shop-api`, invented repository `acme/shop-api`. Timestamps advance
from `2026-10-10T09:00:00Z`; results carry timestamps so durations are derived from them.
