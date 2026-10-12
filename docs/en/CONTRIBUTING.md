# Contributing to AgentLens

> **Language**: [English](CONTRIBUTING.md) | [日本語](../ja/CONTRIBUTING.md)

Thank you for contributing to AgentLens! To ensure efficient collaboration between human developers and AI coding agents, please adhere to the following workflow and standards.

---

## 1. Branching & Pull Request Workflow

All development is strictly **Pull Request (PR) driven**. Direct pushes to `main` are prohibited by branch protection rules.

### 1.1 Branch Protection & Review Policy
- **Branch Protection**: The `main` branch is protected.
- **Review Requirements**: Pull Requests submitted by contributors require **at least 1 approving review from repository maintainers** before merging.
- **Maintainer Privileges**: Repository maintainers have full authority to merge PRs at any time.

### 1.2 Branch Naming Convention
- `feat/<feature-name>`: New features or capabilities
- `fix/<bug-name>`: Bug fixes
- `docs/<doc-name>`: Documentation additions or revisions
- `refactor/<target>`: Code refactoring without behavioral changes
- `test/<test-scope>`: Test additions or enhancements

### 1.3 PR Process
1. Once per clone, enable the git hooks. They run gofmt, `go vet` and a build before each commit, and the full Go test suite before each push, so a red commit never reaches GitHub:
   ```bash
   make hooks
   ```
2. Create a dedicated branch off `main`:
   ```bash
   git checkout -b feat/core-models
   ```
3. Implement changes, write tests, and verify code. `make check` runs what the hooks and CI run for Go (gofmt, vet, build, `go test -race`); the browser tests need Chrome:
   ```bash
   make check
   CHROME_BIN=/path/to/chrome make e2e
   ```
4. Update progress checkboxes in [`docs/en/ROADMAP.md`](../../docs/en/ROADMAP.md) and [`docs/ja/ROADMAP.md`](../../docs/ja/ROADMAP.md).
5. Commit changes with clear, descriptive commit messages.
6. Push your branch and open a PR:
   ```bash
   gh pr create --fill
   ```
7. Fill out all sections of [`.github/pull_request_template.md`](../../.github/pull_request_template.md).
8. Request review from maintainers. Once approved, the PR can be merged into `main`.

---

## 2. Issues & Reporting

- Use [`.github/ISSUE_TEMPLATE/bug_report.md`](../../.github/ISSUE_TEMPLATE/bug_report.md) for reporting unexpected behavior, crashes, or incorrect call graph reconstruction.
- Use [`.github/ISSUE_TEMPLATE/feature_request.md`](../../.github/ISSUE_TEMPLATE/feature_request.md) for suggesting new CLI commands, UI enhancements, or AI provider support.

---

## 3. Language & Documentation Policy

- **Code & Comments**: Strictly **English** (`en`). Variable names, function names, comments, and log messages must be written in English.
- **Documentation**: Maintained in dual English (`docs/en/`) and Japanese (`docs/ja/`). Any change made to one language must be synchronized with the other.
- **User-Facing Strings**: English. A Japanese UI is planned (ROADMAP Phase 8) and not implemented yet.

---

## 4. Architecture Rules
- **Provider Isolation**: Never introduce AI-specific dependencies or hardcoded platform assumptions into `internal/collector`, `graph`, `detector`, or `server`. All AI-specific behavior must reside declaratively within Provider Definitions (`schemas/provider_definition.schema.json`).
