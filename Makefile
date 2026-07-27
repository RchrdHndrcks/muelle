.PHONY: help build install run test test-race test-coverage fmt vet lint check clean dump cross

BINARY := muelle
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary into bin/
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)

install: ## Install the binary into $GOPATH/bin
	go install $(LDFLAGS) ./cmd/$(BINARY)

run: ## Build and run
	go run ./cmd/$(BINARY)

dump: build ## Render a single frame without a terminal (works over a pipe)
	./bin/$(BINARY) -dump -all

test: ## Run all tests
	go test ./...

test-race: ## Run all tests with the race detector
	go test -race ./...

test-coverage: ## Run tests and open the coverage report
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

fmt: ## Format the code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

check: fmt vet test-race ## Format, vet and test with the race detector
	@echo "✅ all checks passed"

cross: ## Cross-compile for linux/amd64 and linux/arm64 (for a server)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64 ./cmd/$(BINARY)
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-arm64 ./cmd/$(BINARY)
	@echo "✅ built for linux/amd64 and linux/arm64"

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out
