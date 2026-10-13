---
name: agentlens
description: Use when a Skill or MCP server seems unused, keeps failing, needs re-authentication, or when the agent fell back to a CLI or curl instead of a tool; also when asked which tools stopped working, why a tool is not being called, or for an overview of recent agent sessions. Queries the local AgentLens analysis of this machine's session logs.
---

# AgentLens: find out which tools stopped working and why

AgentLens analyzes the session logs on this machine (Claude Code and other agents) and knows,
across sessions, which Skills and MCP servers failed, were silently replaced by another tool,
stopped being called, could not connect or wait for a sign-in. Ask it before guessing.

## When to use

- A tool call failed on credentials, a server "needs you to sign in", or a tool you expected to
  use is not in the tool list.
- You (or a previous session) used `gh`, `psql`, `curl` or another CLI instead of an MCP tool.
- The user asks "which MCP servers are broken?", "why isn't X being used?", "what happened in
  my recent sessions?", or wants to verify a fix.

## How to use

Prefer the `agentlens` MCP server when it is registered (tools appear as `mcp__agentlens__*`):

1. `report` with `since` (e.g. `"30d"`) for the overview: totals, per-server counts and the
   **dormant tools** with their reason. Read `dormant_tools` first.
2. `why` with `tool` set to the Skill or MCP server name for one tool: per-session outcomes,
   substitutes, diagnosis, evidence and an `investigation` with `checks` to run on this machine.
3. `session` (summary or `view: "trace"`) and `inspect_call` to look at one session or call.

Without the MCP server, run the CLI with Bash and read the JSON:

```bash
agentlens report --since 30d --json
agentlens why <name> --since 30d --json
```

If `agentlens` is not installed: `go install github.com/tsbkw/agentlens/cmd/agentlens@latest`.
Register the MCP server with `claude mcp add --scope user agentlens -- agentlens mcp`.

## How to act on the answer

- Follow the `investigation.checks` in order. They name the files and commands to inspect for a
  Skill (`SKILL.md`, its description, permissions, plugin) or an MCP server (`claude mcp list`,
  `claude mcp get <name>`, `~/.claude.json`, `.mcp.json`, re-authentication with `/mcp`, token
  location and expiry, permission and instruction files, deferred tools).
- Investigate read-only first; ask before changing settings, credentials or configuration.
- Never print secrets (tokens, keys, passwords). Report where they live and whether they expired.
- Report the root cause with the file, setting or credential that confirms it, the fix steps,
  and how to verify: start a new session, use the tool once, then call `why` again with
  `since: "1d"` and confirm the diagnosis is `healthy`.
- If the diagnosis is `substitute_preferred`, the tool works: look at what steers you toward the
  substitute (instructions, a pre-authorized CLI, a weak tool description) rather than at the server.
- If the diagnosis is `not_found`, check the spelling against `report` and the names in
  `mcp__<server>__` tool names.
