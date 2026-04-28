# hyperstats Makefile
#
# Standard developer commands. All targets are idempotent; run any in
# any order. Tested with GNU make on Linux/macOS; on Windows use WSL2 or
# the direct go commands listed in each target.

.PHONY: help test test-short test-property bench fuzz lint vet cover \
        cover-html clean ci tidy fmt examples demo plots

help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

demo: ## Run cmd/hyperdemo and dump CSVs to docs/data/.
	go run ./cmd/hyperdemo -out docs/data

plots: demo ## Run the demo, then regenerate the README plots.
	@command -v python3 >/dev/null 2>&1 || { \
	  echo "python3 not found; install Python 3 with matplotlib + pandas to regenerate plots"; \
	  exit 1; \
	}
	python3 scripts/plots.py

test: ## Run unit + property tests with race detector.
	go test -race -timeout 5m ./...

test-short: ## Run unit tests only (skip property tests).
	go test -short -race -timeout 1m ./...

test-property: ## Run only the property tests with verbose output.
	go test -timeout 5m -v -run "Empirical|Property|KnownTransition|Adversarial|HeavyTailed|MergeAccuracy|Tails" ./...

bench: ## Run all benchmarks.
	go test -bench=. -benchmem -benchtime=2s -run=^$$ ./...

fuzz-hll: ## Fuzz HLL UnmarshalBinary for 30s.
	go test ./hll/ -run=^$$ -fuzz=FuzzUnmarshal -fuzztime=30s

fuzz-cms: ## Fuzz CMS UnmarshalBinary for 30s.
	go test ./cms/ -run=^$$ -fuzz=FuzzUnmarshal -fuzztime=30s

fuzz-kll: ## Fuzz KLL UnmarshalBinary for 30s.
	go test ./kll/ -run=^$$ -fuzz=FuzzUnmarshal -fuzztime=30s

fuzz-tdigest: ## Fuzz t-digest UnmarshalBinary for 30s.
	go test ./tdigest/ -run=^$$ -fuzz=FuzzUnmarshal -fuzztime=30s

fuzz: fuzz-hll fuzz-cms fuzz-kll fuzz-tdigest ## Fuzz all UnmarshalBinary entry points.

lint: ## Run golangci-lint (requires it to be installed).
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint not installed; install via:"; \
	  echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	  exit 1; \
	}
	golangci-lint run ./...

vet: ## Run go vet.
	go vet ./...

fmt: ## Format all Go files with gofmt.
	gofmt -w -s .

cover: ## Run tests with coverage profile.
	go test -timeout 5m -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1

cover-html: cover ## Generate HTML coverage report.
	go tool cover -html=coverage.out -o coverage.html
	@echo "Open coverage.html in a browser."

examples: ## Run all examples.
	@for ex in unique_visitors heavy_hitters latency_quantiles; do \
	  echo "=== Running examples/$$ex ==="; \
	  go run ./examples/$$ex; \
	  echo; \
	done

tidy: ## Tidy go.mod / go.sum.
	go mod tidy

ci: vet test cover ## Full CI gate: vet + race tests + coverage.

clean: ## Remove build artifacts.
	go clean ./...
	rm -f coverage.out coverage.html
