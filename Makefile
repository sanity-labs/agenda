# Colour for the help listing. Named rather than > and <: inside a recipe
# $< is make's own "first prerequisite", and comes out empty.
CYAN  := $(shell printf '\033[1;36m')
RESET := $(shell printf '\033[0m')

# What GoReleaser stamps on a release build, so a local build says who it
# is too; `agenda version` prints these.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
BIN     ?= bin/agenda

.DEFAULT_GOAL := help
.PHONY: help build install run test vet fmt fmt-check check lint deadcode snapshot clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-10s$(RESET) %s\n", $$1, $$2}'

build: ## Build bin/agenda with version info
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

install: ## go install with version info
	go install -trimpath -ldflags "$(LDFLAGS)" .

run: ## Run from source (the commands live in cli.go, so not main.go)
	go run .

test: ## Run the suite with the race detector, as CI does
	go test -race ./...

vet: ## go vet
	go vet ./...

fmt: ## gofmt the tree
	gofmt -w .

fmt-check: ## Fail on files that are not gofmt'd, as CI does
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt'd:"; echo "$$unformatted"; exit 1; \
	fi

check: fmt-check vet test lint ## Everything CI runs: formatting, vet, tests, lint

lint: ## golangci-lint (.golangci.yml: the standard set)
	golangci-lint run ./...

deadcode: ## Report unreachable functions (the audit keeps this empty)
	go run golang.org/x/tools/cmd/deadcode@latest -test ./...

snapshot: ## Local dry run of the release pipeline (needs goreleaser)
	goreleaser release --snapshot --clean

clean: ## Remove build output
	rm -rf bin/ dist/
