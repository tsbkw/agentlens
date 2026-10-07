# AgentLens: 開発ロードマップ & 進捗管理 (Development Roadmap & Progress Tracking)

> **言語切り替え**: [English](../en/ROADMAP.md) | [日本語](ROADMAP.md)

本ドキュメントは、AgentLens の全機能、開発フェーズ、およびPull Request（PR）の進捗状況を一元管理するファイルです。AI駆動開発において、このファイルを「進捗の唯一の真実の源（Single Source of Truth）」として維持し、PR作成・マージ時にチェックボックスとPR番号を更新します。

---

## 全体進捗ステータス

| フェーズ | 概要 | 状態 | 対象 / PR |
| :--- | :--- | :--- | :--- |
| **Phase 0** | プロジェクト基盤、仕様策定、Goアーキテクチャ、AI協調開発環境、PR運用確立 | 完了 | PR #1 |
| **Phase 1** | コアデータモデル、プロバイダ定義スキーマ & YAMLローダー | 完了 | PR #2 |
| **Phase 2** | トレース収集エンジン & コールグラフ構築器 (DAG in Go) | 完了 | PR #3 |
| **Phase 3** | 異常・暗黙フォールバック検知エンジン | 完了 | PR #4 |
| **Phase 4** | CLI 可視化 (`agentlens graph`, `inspect`, `watch`) | 完了 | PR #5 |
| **Phase 5** | Web UI ダッシュボード (バイナリ内蔵 & 無料GitHub Pagesビューアー) | 完了 | PR #6 |
| **Phase 6** | 各種生成AI向けプロバイダ定義 (Antigravity, Claude Code, Cursor 等) | 計画中 | PR #7 |
| **Phase 7** | 多言語化 (i18n: 英語・日本語) CLI/UIローカライズ | 計画中 | PR #8 |

---

## フェーズ別タスク詳細

### Phase 0: プロジェクト基盤 & 仕様策定 (PR #1)
- [x] リポジトリ構成、`.gitignore`、および `go.mod` の策定
- [x] 日英バイリンガル機能仕様書の作成 (`docs/en/SPECIFICATION.md`, `docs/ja/SPECIFICATION.md`)
- [x] 日英バイリンガルアーキテクチャ設計書の作成 (`docs/en/ARCHITECTURE.md`, `docs/ja/ARCHITECTURE.md`)
- [x] プロバイダ定義 JSON Schema の策定 (`schemas/provider_definition.schema.json`)
- [x] サンプルプロバイダ定義の作成 (`examples/providers/antigravity.yaml`, `generic_jsonl.yaml`)
- [x] GitHub テンプレート（PRテンプレート、Issueテンプレート）の配備
- [x] AIエージェント向け開発指針の配備 (`AGENTS.md`, `.github/copilot-instructions.md`, `.cursorrules`)
- [x] GitHub Actions CI (`.github/workflows/ci.yml`) および Pages デプロイ (`deploy-pages.yml`) の設定
- [x] GitHub ブランチ保護設定（メンテナーはいつでもマージ可能、他コントリビューターは要Approve）
- [x] Go コアデータモデル (`internal/models/`) および CLI エントリーポイント (`cmd/agentlens/`) の実装
- [x] PRベースの開発ワークフローの確立と初期PRのマージ

### Phase 1: コアデータモデル & プロバイダローダー (PR #2)
- [x] プロバイダ定義ローダーの実装 (`internal/providers/loader.go`, YAMLバリデーション)
- [x] セッションおよびトレースデータパーサーの実装 (`internal/providers/parser.go`)
- [x] YAMLスキーマ検証およびプロバイダパースのユニットテスト作成

### Phase 2: 収集エンジン & コールグラフ構築器 (PR #3)
- [x] `BaseCollector` および JSONL ログストリームリーダーの実装 (`internal/collector/`)
- [x] Go言語による DAG 再構築 `GraphBuilder` の実装 (`internal/graph/`)
- [x] サブエージェント呼び出し・会話ターンの階層構造結合ロジックの実装
- [x] 模擬トレースを用いたユニットテスト作成

### Phase 3: 異常・フォールバック検知エンジン (PR #4)
- [x] `AuthExpirationChecker` (401/403/トークン失効パターンの検知) 実装
- [x] `SilentFallbackDetector` (失敗ツールからシェルや代替ツールへの暗黙遷移検知) 実装
- [x] `RetryLoopDetector` (連続失敗・無駄なリトライループの検知) 実装
- [x] 異常検知ルールとエッジ付与 (`FALLBACK_TO`) のユニットテスト作成

### Phase 4: CLI 可視化 (PR #5)
- [x] `agentlens list` (セッションおよびトレース一覧表示) 実装
- [x] `agentlens graph <session_id>` (ターミナルツリー型コールグラフ描画) 実装
- [x] `agentlens inspect <call-id>` (ノード詳細、引数、出力、異常レポート表示) 実装
- [x] デフォルトの Antigravity / Gemini プロバイダ設定のバイナリ内蔵化

### Phase 5: Web UI ダッシュボード (PR #6)
- [x] Go内蔵ローカルHTTPサーバー (`agentlens ui`) とWebアセットの組み込み (`go:embed`) 実装
- [x] `web/index.html` におけるインタラクティブ DAG グラフ可視化と統計表示の実装
- [x] 異常警告バナーおよびインスペクタードロワーの実装
- [x] GitHub Pages 上での完全クライアントサイド（ドラッグ＆ドロップ）可視化の有効化

### Phase 6: マルチプロバイダ対応 (PR #7)
- [ ] Antigravity transcript パーサーおよびライブログ監視
- [ ] Claude Code セッショントレースアダプター
- [ ] Cursor / Roo Code トレースアダプター
- [ ] カスタムプロバイダ作成ガイドドキュメントの整備

### Phase 7: 完全多言語化 & 仕上げ (PR #8)
- [ ] 多言語管理モジュール実装 (英語・日本語)
- [ ] CLI出力およびWeb UI文言のローカライズ
- [ ] E2E統合テストおよび総合ドキュメントの完成
