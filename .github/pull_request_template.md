## Description / 概要

<!-- Provide a concise explanation of what this PR introduces or changes. -->
<!-- このPRが導入または変更する内容の概要を簡潔に記述してください。 -->

## Motivation & Context / 目的と背景

<!-- Why is this change required? What problem does it solve? -->
<!-- なぜこの変更が必要なのか、どの課題を解決するのかを記述してください。 -->

## Architectural Component Impact / 影響コンポーネント

- [ ] Provider Definitions (`schemas/`, `examples/providers/`, `rules/`)
- [ ] Core Models (`internal/models`)
- [ ] Collector (`internal/collector`, `internal/providers`)
- [ ] Graph Builder (`internal/graph`)
- [ ] Anomaly Detector (`internal/detector`)
- [ ] CLI (`cmd/agentlens`, `internal/cli`, `internal/watcher`, `internal/report`)
- [ ] Web UI (`web/index.html`, mirrored in `internal/server/static/index.html`, `internal/server`)
- [ ] Documentation / Specs (`docs/`, `README.md`, `README.ja.md`)

## Related Roadmap Tasks / 関連ロードマップタスク

<!-- Link the Phase and Task in docs/en/ROADMAP.md and docs/ja/ROADMAP.md -->
- Phase: 
- Task: 

## Verification & Testing / 検証・テスト結果

<!-- Describe the tests or commands executed to verify this change. -->
- [ ] Unit tests pass (`go test -race ./...`)
- [ ] Static checks pass (`go vet ./...`, `gofmt -l .` prints nothing)
- [ ] Web UI changes: `web/index.html` copied to `internal/server/static/index.html` and the E2E suite passes (`npm --prefix tests/e2e test`)
- [ ] Documentation updated in both English and Japanese

## Screenshots / CLI Output (if applicable)

<!-- Attach terminal outputs or screenshots for UI/CLI changes. -->
