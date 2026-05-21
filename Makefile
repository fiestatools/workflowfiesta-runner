BINARY_NAME := workflowfiesta-runner
MODULE      := workflowfiesta-runner
CMD_PKG     := ./cmd/runner

# Version: use git tag if available, else "dev"
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -s -w -X main.version=$(VERSION)

GO          := go
GOFLAGS     ?=
CGO_ENABLED ?= 0

GOOS        ?= $(shell $(GO) env GOOS)
GOARCH      ?= $(shell $(GO) env GOARCH)

DIST        := dist

.PHONY: help build build-gui build-headless test test-race test-cover lint fmt vet \
        clean docker run run-local install \
        build-linux-headless build-linux-gui \
        build-darwin-headless build-darwin-gui \
        build-windows-headless build-windows-gui \
        build-all check

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}'

build: build-headless ## Build headless binary (default)

build-headless: ## Build headless binary (no GUI, no CGO)
	CGO_ENABLED=0 $(GO) build -tags nolocalui -ldflags "$(LDFLAGS)" \
		-o $(BINARY_NAME) $(CMD_PKG)

build-gui: ## Build GUI binary (requires CGO + system deps)
	CGO_ENABLED=1 $(GO) build -ldflags "$(LDFLAGS)" \
		-o $(BINARY_NAME)-gui $(CMD_PKG)

$(DIST):
	mkdir -p $(DIST)

build-linux-headless: $(DIST) ## Cross-compile: Linux headless (amd64)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -tags nolocalui \
		-ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_NAME)-linux-amd64 $(CMD_PKG)

build-linux-gui: $(DIST) ## Cross-compile: Linux GUI (amd64, requires cross-deps)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GO) build \
		-ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_NAME)-linux-amd64-gui $(CMD_PKG)

build-darwin-headless: $(DIST) ## Cross-compile: macOS headless (arm64 + amd64)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -tags nolocalui \
		-ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_NAME)-darwin-arm64 $(CMD_PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -tags nolocalui \
		-ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_NAME)-darwin-amd64 $(CMD_PKG)

build-darwin-gui: $(DIST) ## Build macOS GUI (native arch only)
	CGO_ENABLED=1 GOOS=darwin GOARCH=$(GOARCH) $(GO) build \
		-ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_NAME)-darwin-$(GOARCH)-gui $(CMD_PKG)

build-windows-headless: $(DIST) ## Cross-compile: Windows headless (amd64)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -tags nolocalui \
		-ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_NAME)-windows-amd64.exe $(CMD_PKG)

build-windows-gui: $(DIST) ## Cross-compile: Windows GUI (amd64, requires mingw)
	CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 $(GO) build \
		-ldflags "$(LDFLAGS) -H windowsgui" -o $(DIST)/$(BINARY_NAME)-windows-amd64-gui.exe $(CMD_PKG)

build-all: build-linux-headless build-darwin-headless build-windows-headless ## Build all headless variants

test: ## Run all tests
	$(GO) test ./...

test-race: ## Run tests with race detector
	CGO_ENABLED=1 $(GO) test -race ./...

test-cover: ## Run tests with coverage report
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out
	@echo "\nHTML report: go tool cover -html=coverage.out"

lint: ## Run golangci-lint (install: https://golangci-lint.run/usage/install/)
	@which golangci-lint > /dev/null 2>&1 || \
		(echo "golangci-lint not found. Install: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

fmt: ## Format all Go source files
	$(GO) fmt ./...
	@echo "Formatted."

vet: ## Run go vet
	$(GO) vet ./...

check: fmt vet test ## Run fmt + vet + tests (pre-commit check)

docker: ## Build Docker image
	docker build -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .

docker-run: docker ## Build and run in Docker
	docker run --rm -it \
		-e WORKFLOWFIESTA_TOKEN \
		-e WORKFLOWFIESTA_API_URL \
		-e WORKFLOWFIESTA_RUNNER_ID \
		-e WORKFLOWFIESTA_RUNNER_NAME \
		-v /var/run/docker.sock:/var/run/docker.sock \
		$(BINARY_NAME):latest run

run: build-headless ## Build and run (headless)
	./$(BINARY_NAME) run

run-local: build-headless ## Build and run in local mode (headless)
	./$(BINARY_NAME) run-local --headless

install: ## Install binary to $GOPATH/bin
	CGO_ENABLED=0 $(GO) install -tags nolocalui -ldflags "$(LDFLAGS)" $(CMD_PKG)

deps: ## Download and tidy dependencies
	$(GO) mod download
	$(GO) mod tidy

deps-update: ## Update all dependencies to latest
	$(GO) get -u ./...
	$(GO) mod tidy


clean: ## Remove build artifacts
	rm -f $(BINARY_NAME) $(BINARY_NAME)-gui
	rm -rf $(DIST)
	rm -f coverage.out
