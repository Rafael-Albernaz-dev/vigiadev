.PHONY: all quality-gate test-regression test vet lint build smoke-test clean

BIN := bin/vigiadev

all: quality-gate

vet:
	@echo "🔍 Running go vet..."
	go vet ./...

lint:
	@echo "🔍 Running linter..."
	@if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "ℹ️ golangci-lint not installed, skipping..."; \
	fi

test:
	@echo "🧪 Running full test suite with race detector..."
	go test -v -race ./...

test-regression:
	@echo "🧪 Running regression tests with race detector..."
	go test -v -race ./test/regression/...

build:
	@echo "🔨 Building vigiadev binary into $(BIN)..."
	@mkdir -p bin
	go build -o $(BIN) ./cmd/vigiadev
	@GOBIN_DIR="$$(go env GOPATH)/bin"; \
	if [ -d "$$GOBIN_DIR" ] && [ -w "$$GOBIN_DIR" ]; then \
		cp $(BIN) "$$GOBIN_DIR/vigiadev"; \
		echo "📦 Synced binary to $$GOBIN_DIR/vigiadev"; \
	fi

smoke-test: build
	@echo "🚀 Running smoke test against atlas modular monolith fixture..."
	@./scripts/smoke_test.sh

quality-gate: vet lint test build smoke-test
	@echo ""
	@echo "========================================================"
	@echo "✨ QUALITY GATE PASSED: All checks passed with 100% success!"
	@echo "========================================================"

clean:
	@rm -rf bin/
