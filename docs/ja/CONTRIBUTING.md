# AgentLens 開発・コントリビューション規約 (Contributing Guidelines)

> **言語切り替え**: [English](../en/CONTRIBUTING.md) | [日本語](CONTRIBUTING.md)

AgentLens へのご貢献ありがとうございます！ 人間とAIエージェントがスムーズに協調開発を行うため、以下の開発ワークフローおよびコーディング規約を厳守してください。

---

## 1. ブランチ戦略とプルリクエスト (PR) 運用

開発は原則として **プルリクエスト (PR) ベース** で実施します。ブランチ保護ルールにより、`main` への直接プッシュは禁止されています。

### 1.1 ブランチ保護とレビューポリシー
- **ブランチ保護**: `main` ブランチは保護されています。
- **レビュー要件**: コントリビューターが作成したPRは、**メンテナーによるApprove（承認）が最低1件必要** です。
- **メンテナー権限**: リポジトリメンテナーは、いつでもPRのレビュー、承認、およびマージを行う権限を有します。

### 1.2 ブランチ命名規則
- `feat/<feature-name>`: 新機能の追加
- `fix/<bug-name>`: バグ修正
- `docs/<doc-name>`: ドキュメントの追加・改訂
- `refactor/<target>`: 振る舞いを変えないコードリファクタリング
- `test/<test-scope>`: テストコードの追加・強化

### 1.3 PR の作成・マージ手順
1. `main` ブランチから機能ブランチを作成:
   ```bash
   git checkout -b feat/core-models
   ```
2. 機能実装、テスト追加、静的解析を実行:
   ```bash
   go test ./...
   go vet ./...
   cd tests/e2e && npm test && cd ../..
   ```
3. [`docs/en/ROADMAP.md`](file:///home/tsbkw0/development/agentlens/docs/en/ROADMAP.md) および [`docs/ja/ROADMAP.md`](file:///home/tsbkw0/development/agentlens/docs/ja/ROADMAP.md) のタスク進捗チェックボックスを更新。
4. 明確でわかりやすいコミットメッセージを作成。
5. リモートへプッシュし、GitHub CLI または Web UI で PR を作成:
   ```bash
   gh pr create --fill
   ```
6. [`.github/pull_request_template.md`](file:///home/tsbkw0/development/agentlens/.github/pull_request_template.md) のフォーマットに従って変更内容を記述。
7. メンテナーのレビューを経て、承認後に `main` へマージ。

---

## 2. Issueの起票ガイド

- 不具合や予期せぬ動作、クラッシュを発見した場合は [`.github/ISSUE_TEMPLATE/bug_report.md`](file:///home/tsbkw0/development/agentlens/.github/ISSUE_TEMPLATE/bug_report.md) を使用してください。
- 新機能の提案や新しい生成AIツールのサポート要望は [`.github/ISSUE_TEMPLATE/feature_request.md`](file:///home/tsbkw0/development/agentlens/.github/ISSUE_TEMPLATE/feature_request.md) を使用してください。

---

## 3. 言語およびドキュメント管理規約

- **コードおよびコメント**: **英語 (English)** で統一。変数名、関数名、コメント、ログ出力は英語で記述してください。
- **ドキュメント**: 英語 (`docs/en/`) を正本とし、日本語 (`docs/ja/`) を完全対応で同期維持します。いずれかを更新した場合は、必ずもう一方も同期してください。
- **ユーザー向け表示 (CLI / UI)**: デフォルトは英語とし、日英ローカライズをサポートします。

---

## 4. アーキテクチャ原則
- **プロバイダ依存の完全分離**: `internal/collector`、`graph`、`detector`、`server` に特定のAI製品（Antigravity、Claude Code、Cursor等）に依存したロジックを直接埋め込むことは禁止です。AI依存の差分は、すべて宣言的プロバイダ定義 (`schemas/provider_definition.schema.json`) に集約してください。
