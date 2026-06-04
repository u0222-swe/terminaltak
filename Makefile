.DEFAULT_GOAL := build

LDFLAGS   ?= -s -w
BINARY    := terminaltak
DIST_DIR  := dist

# --- Go toolchain selection -------------------------------------------------
# The module pins a minimum Go version (see the `go` line in go.mod) that
# carries security fixes. Rather than force every contributor to upgrade their
# system Go, we resolve a Go command that satisfies it:
#   1. an explicit `GO=...` override always wins;
#   2. else the system `go`, if it is already >= GO_VERSION;
#   3. else the pinned toolchain downloaded via `golang.org/dl` into GOPATH/bin
#      (auto-installed by the `toolchain` target the first time it is needed).
GO_VERSION ?= 1.25.11
GODL       := go$(GO_VERSION)
GOPATH_BIN := $(shell go env GOPATH 2>/dev/null)/bin
PINNED_GO  := $(GOPATH_BIN)/$(GODL)

# "yes" when the system `go` already meets GO_VERSION.
SYS_GO_OK := $(shell go version 2>/dev/null | awk '{v=$$3; sub(/^go/,"",v); n=split(v,a,"."); split("$(GO_VERSION)",b,"."); pat=(n>=3?a[3]:0); if (a[1]>b[1] || (a[1]==b[1] && (a[2]>b[2] || (a[2]==b[2] && pat>=b[3])))) print "yes"}')

ifndef GO
  ifeq ($(SYS_GO_OK),yes)
    GO := go
  else
    GO := $(PINNED_GO)
  endif
endif

.PHONY: toolchain
toolchain: ## Download the pinned Go toolchain (go$(GO_VERSION)) into GOPATH/bin
	go install golang.org/dl/$(GODL)@latest
	"$(PINNED_GO)" download

# ensure-toolchain guarantees $(GO) exists and runs; if the resolved command is
# the pinned toolchain and it is not yet installed, install it automatically.
.PHONY: ensure-toolchain
ensure-toolchain:
	@if ! "$(GO)" version >/dev/null 2>&1; then \
	  echo ">> $(GODL) required by go.mod but not installed — fetching it..."; \
	  $(MAKE) --no-print-directory toolchain; \
	fi
	@echo "Using $$("$(GO)" version)"

.PHONY: build
build: ensure-toolchain ## Build the binary for the current platform
	CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/terminaltak

.PHONY: run
run: ensure-toolchain ## Build and run terminaltak
	$(GO) run ./cmd/terminaltak

.PHONY: test
test: ensure-toolchain ## Run all unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ensure-toolchain ## Run all unit tests with the race detector
	$(GO) test -race ./...

.PHONY: vet
vet: ensure-toolchain ## go vet over every package
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
release: ensure-toolchain ## Cross-compile for linux + darwin, amd64 + arm64
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
