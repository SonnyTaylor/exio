# Exio Makefile
# Cross-platform build targets for client and server

VERSION ?= 1.0.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)"

# Output directories
BIN_DIR := bin
DIST_DIR := dist

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOCLEAN := $(GOCMD) clean
GOMOD := $(GOCMD) mod

# Platforms for cross-compilation
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build build-client build-server build-all test clean deps lint help

# Default target
all: deps test build

# Build for current platform
build: build-client build-server

build-client:
	@echo "Building exio client..."
	$(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/exio ./cmd/exio

build-server:
	@echo "Building exiod server..."
	$(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/exiod ./cmd/exiod

# Cross-compile for all platforms
build-all: clean
	@echo "Building for all platforms..."
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} \
		$(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/exio-$${platform%/*}-$${platform#*/}$$([ "$${platform%/*}" = "windows" ] && echo ".exe") ./cmd/exio; \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} \
		$(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/exiod-$${platform%/*}-$${platform#*/}$$([ "$${platform%/*}" = "windows" ] && echo ".exe") ./cmd/exiod; \
		echo "  Built: $${platform}"; \
	done
	@echo "All builds complete in $(DIST_DIR)/"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -cover ./...

# Run tests with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -rf $(BIN_DIR) $(DIST_DIR)
	rm -f coverage.out coverage.html

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Run linter (requires golangci-lint)
lint:
	@echo "Running linter..."
	golangci-lint run ./...

# Install binaries to GOPATH/bin
install: build
	@echo "Installing..."
	cp $(BIN_DIR)/exio $(GOPATH)/bin/
	cp $(BIN_DIR)/exiod $(GOPATH)/bin/

# Development: run server locally
run-server:
	EXIO_PORT=8080 EXIO_TOKEN=dev-token EXIO_BASE_DOMAIN=localhost \
	$(GOCMD) run ./cmd/exiod

# Development: run client locally
run-client:
	EXIO_SERVER=http://localhost:8080 EXIO_TOKEN=dev-token \
	$(GOCMD) run ./cmd/exio http 3000 --subdomain test

# Help
help:
	@echo "Exio Build System"
	@echo ""
	@echo "Usage:"
	@echo "  make              - Run deps, test, and build"
	@echo "  make build        - Build client and server for current platform"
	@echo "  make build-all    - Cross-compile for all platforms"
	@echo "  make test         - Run tests with race detection"
	@echo "  make test-coverage- Run tests and generate coverage report"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make deps         - Download and tidy dependencies"
	@echo "  make lint         - Run golangci-lint"
	@echo "  make install      - Install binaries to GOPATH/bin"
	@echo "  make run-server   - Run server in development mode"
	@echo "  make run-client   - Run client in development mode"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)"
