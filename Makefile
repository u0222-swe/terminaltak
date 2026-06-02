.DEFAULT_GOAL := build

GO        ?= go
LDFLAGS   ?= -s -w
BINARY    := terminaltak
DIST_DIR  := dist

.PHONY: build
build: ## Build the binary for the current platform
	CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/terminaltak

.PHONY: run
run: ## Build and run terminaltak
	$(GO) run ./cmd/terminaltak

.PHONY: test
test: ## Run all unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run all unit tests with the race detector
	$(GO) test -race ./...

.PHONY: vet
vet: ## go vet over every package
	$(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint (requires golangci-lint on PATH)
	golangci-lint run ./...

.PHONY: tidy
tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

.PHONY: fmt
fmt: ## gofmt every file in place
	$(GO) fmt ./...

.PHONY: release
release: ## Cross-compile for linux + darwin, amd64 + arm64
	@mkdir -p $(DIST_DIR)
	GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-amd64  ./cmd/terminaltak
	GOOS=linux  GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-arm64  ./cmd/terminaltak
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-darwin-amd64 ./cmd/terminaltak
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-darwin-arm64 ./cmd/terminaltak

.PHONY: clean
clean: ## Remove build artefacts
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
