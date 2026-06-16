BINARY_NAME := cortex-git-server
BIN_DIR     := bin
MODULE      := github.com/cortex-sync/Cortex/mcp/git-server

# Pinned golangci-lint; keep in step with the lint job in .github/workflows/ci.yml.
GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT := $(shell go env GOPATH)/bin/golangci-lint

.PHONY: fmt test lint validate build build-all clean

fmt:
	gofmt -w .

test:
	go test ./...

# Installs the pinned golangci-lint on first use (via GOPROXY), then lints the
# default and e2e build-tag variants.
lint:
	@command -v $(GOLANGCI_LINT) >/dev/null || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) run --build-tags e2e ./...

validate: lint
	go vet ./...
	go build ./...
	go test ./...

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/server

build-all:
	mkdir -p $(BIN_DIR)
	GOOS=darwin  GOARCH=amd64  go build -o $(BIN_DIR)/$(BINARY_NAME)-darwin-amd64  ./cmd/server
	GOOS=darwin  GOARCH=arm64  go build -o $(BIN_DIR)/$(BINARY_NAME)-darwin-arm64  ./cmd/server
	GOOS=linux   GOARCH=amd64  go build -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64   ./cmd/server
	GOOS=windows GOARCH=amd64  go build -o $(BIN_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/server

clean:
	rm -rf $(BIN_DIR)
