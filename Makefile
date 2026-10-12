# Developer entry points. CI runs the same checks (.github/workflows/ci.yml), and the git hooks
# in .githooks/ run a subset before each commit and push. Enable the hooks once per clone with
# `make hooks`.

GO ?= go
CHROME_BIN ?=

.PHONY: help fmt fmt-check vet build test e2e tidy-check check hooks

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

fmt: ## Format all Go files in place
	gofmt -w .

fmt-check: ## Fail if any Go file is not gofmt-formatted
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-formatted (run 'make fmt'):"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi

vet: ## Run go vet
	$(GO) vet ./...

build: ## Compile every package and the agentlens binary
	$(GO) build ./...
	$(GO) build -o /dev/null ./cmd/agentlens

test: ## Run the Go tests with the race detector
	$(GO) test -race ./...

tidy-check: ## Fail if go.mod or go.sum would change under go mod tidy
	@cp go.mod go.mod.tidy-check && cp go.sum go.sum.tidy-check; \
	$(GO) mod tidy; \
	status=0; \
	if ! cmp -s go.mod go.mod.tidy-check || ! cmp -s go.sum go.sum.tidy-check; then \
		echo "go.mod / go.sum are not tidy (run 'go mod tidy')"; status=1; \
	fi; \
	mv go.mod.tidy-check go.mod; mv go.sum.tidy-check go.sum; \
	exit $$status

e2e: ## Run the browser E2E tests (needs Chrome; set CHROME_BIN to its binary)
	npm --prefix tests/e2e install --no-audit --no-fund
	CHROME_BIN="$(CHROME_BIN)" npm --prefix tests/e2e test

check: fmt-check vet build test ## Everything the pre-push hook runs

hooks: ## Make git run the hooks in .githooks/ for this clone
	git config core.hooksPath .githooks
	@echo "Git hooks enabled: .githooks/pre-commit and .githooks/pre-push"
