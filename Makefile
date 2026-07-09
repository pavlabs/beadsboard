BINARY  := beadsboard
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
SOURCE  ?= .

.DEFAULT_GOAL := help

.PHONY: help build run test test-race vet lint fmt check install tidy clean

help: ## List available targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the version-stamped binary
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: ## Run against SOURCE (e.g. make run SOURCE=~/code/projects/bilder)
	go run . --source $(SOURCE)

test: ## Run the tests
	go test ./...

test-race: ## Run the tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format with gofumpt
	gofumpt -w .

check: fmt vet build test lint ## Run the full quality gate

install: ## Install to GOBIN
	go install -ldflags "$(LDFLAGS)" .

tidy: ## Tidy go.mod/go.sum
	go mod tidy

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist
