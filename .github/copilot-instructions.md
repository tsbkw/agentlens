# GitHub Copilot / Coding Assistant Instructions

See `AGENTS.md` for full system architecture and instructions.

## Key Rules
1. **Architecture Isolation**:
   - Provider definitions (`schemas/provider_definition.schema.json` and YAML files) are the ONLY place containing AI platform-specific formats.
   - Core Go code in `internal/` must be 100% provider-agnostic.
2. **Code & Comments**: Strictly in English.
3. **Documentation**: Maintained in dual English (`docs/en/`) and Japanese (`docs/ja/`). Keep both synchronized.
4. **Workflow**:
   - Always work on dedicated feature branches (`feat/*`, `fix/*`, `docs/*`).
   - Run `go test ./...` and `go vet ./...` before submitting changes.
   - Update `docs/en/ROADMAP.md` and `docs/ja/ROADMAP.md` task checklists.
   - Submit changes via Pull Requests using `.github/pull_request_template.md`.
