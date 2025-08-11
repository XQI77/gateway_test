# Makefile for gatesvr

# Project info
PROJECT_NAME := gatesvr
BINARY_NAME := gatesvr
UPSTREAM_BINARY_NAME := upstream
VERSION := v1.0.0
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GO_VERSION := $(shell go version | cut -d ' ' -f 3)

# Directories
BUILD_DIR := build
CMD_DIR := cmd/gatesvr
UPSTREAM_CMD_DIR := cmd/upstream
PROTO_DIR := proto

# Go build flags
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GoVersion=$(GO_VERSION)"
GO_FLAGS := -v

# Default target
.PHONY: all
all: clean fmt vet test build build-upstream

# Build the gateway binary
.PHONY: build
build:
	@echo "Building $(PROJECT_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build $(GO_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)
	@echo "Build completed: $(BUILD_DIR)/$(BINARY_NAME)"

# Build the upstream binary
.PHONY: build-upstream
build-upstream:
	@echo "Building upstream service..."
	@mkdir -p $(BUILD_DIR)
	@go build $(GO_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME) ./$(UPSTREAM_CMD_DIR)
	@echo "Build completed: $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME)"

# Build both binaries
.PHONY: build-both
build-both: build build-upstream

# Build for multiple platforms
.PHONY: build-all
build-all: clean
	@echo "Building for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@echo "Building Gateway for Linux amd64..."
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME)-linux-amd64 ./$(UPSTREAM_CMD_DIR)
	@echo "Building Gateway for Windows amd64..."
	@GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)
	@GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME)-windows-amd64.exe ./$(UPSTREAM_CMD_DIR)
	@echo "Building Gateway for macOS amd64..."
	@GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./$(CMD_DIR)
	@GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME)-darwin-amd64 ./$(UPSTREAM_CMD_DIR)
	@echo "Building Gateway for macOS arm64..."
	@GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)
	@GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME)-darwin-arm64 ./$(UPSTREAM_CMD_DIR)

# Run the gateway application
.PHONY: run
run:
	@go run ./$(CMD_DIR)

# Run with config file
.PHONY: run-config
run-config:
	@go run ./$(CMD_DIR) -config=test-config.yaml

# Run upstream service
.PHONY: run-upstream
run-upstream:
	@go run ./$(UPSTREAM_CMD_DIR) --zone=001 --addr=:9001 --gateway=localhost:8092

# Run upstream service for specific zone
.PHONY: run-upstream-zone
run-upstream-zone:
	@echo "Usage: make run-upstream-zone ZONE=001 PORT=9001"
	@go run ./$(UPSTREAM_CMD_DIR) --zone=$(ZONE) --addr=:$(PORT) --gateway=localhost:8092

# Run development environment (gateway + 3 upstream zones)
.PHONY: run-dev
run-dev:
	@echo "Starting development environment..."
	@echo "Starting Gateway..."
	@go run ./$(CMD_DIR) -config=test-config.yaml &
	@sleep 2
	@echo "Starting Upstream Zone 001..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=001 --addr=:9001 --gateway=localhost:8092 &
	@echo "Starting Upstream Zone 002..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=002 --addr=:9002 --gateway=localhost:8092 &
	@echo "Starting Upstream Zone 003..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=003 --addr=:9003 --gateway=localhost:8092 &
	@echo "Development environment started. Press Ctrl+C to stop all services."
	@trap 'kill %1 %2 %3 %4' INT; wait

# Test
.PHONY: test
test:
	@echo "Running tests..."
	@go test -v ./...

# Test with coverage
.PHONY: test-cover
test-cover:
	@echo "Running tests with coverage..."
	@go test -v -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Benchmark tests
.PHONY: bench
bench:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./...

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Vet code
.PHONY: vet
vet:
	@echo "Vetting code..."
	@go vet ./...

# Lint code (requires golangci-lint)
.PHONY: lint
lint:
	@echo "Linting code..."
	@golangci-lint run

# Generate protobuf files
.PHONY: proto
proto:
	@echo "Generating protobuf files..."
	@protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/*.proto

# Install dependencies
.PHONY: deps
deps:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy

# Install development tools
.PHONY: tools
tools:
	@echo "Installing development tools..."
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html
	@rm -f gatesvr.log*
	@rm -f upstream.log*

# Docker build
.PHONY: docker
docker:
	@echo "Building Docker image..."
	@docker build -t $(PROJECT_NAME):$(VERSION) .
	@docker build -t $(PROJECT_NAME):latest .

# Docker run
.PHONY: docker-run
docker-run:
	@echo "Running Docker container..."
	@docker run -d --name $(PROJECT_NAME) \
		-p 8443:8443 \
		-p 8080:8080 \
		-p 9090:9090 \
		-p 9100:9100 \
		$(PROJECT_NAME):latest

# Generate certificates for development
.PHONY: certs
certs:
	@echo "Generating development certificates..."
	@mkdir -p certs
	@openssl req -new -newkey rsa:4096 -days 365 -nodes -x509 \
		-subj "/C=US/ST=CA/L=San Francisco/O=GateSvr/CN=localhost" \
		-keyout certs/server.key \
		-out certs/server.crt

# Development setup
.PHONY: setup
setup: tools deps proto certs
	@echo "Development environment setup completed"

# Install the gateway binary
.PHONY: install
install: build
	@echo "Installing $(PROJECT_NAME)..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/

# Install the upstream binary
.PHONY: install-upstream
install-upstream: build-upstream
	@echo "Installing upstream service..."
	@cp $(BUILD_DIR)/$(UPSTREAM_BINARY_NAME) $(GOPATH)/bin/

# Install both binaries
.PHONY: install-both
install-both: install install-upstream

# Uninstall the gateway binary
.PHONY: uninstall
uninstall:
	@echo "Uninstalling $(PROJECT_NAME)..."
	@rm -f $(GOPATH)/bin/$(BINARY_NAME)

# Uninstall the upstream binary
.PHONY: uninstall-upstream
uninstall-upstream:
	@echo "Uninstalling upstream service..."
	@rm -f $(GOPATH)/bin/$(UPSTREAM_BINARY_NAME)

# Uninstall both binaries
.PHONY: uninstall-both
uninstall-both: uninstall uninstall-upstream

# Show help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all              - Format, vet, test and build both services"
	@echo ""
	@echo "Build targets:"
	@echo "  build            - Build the gateway binary"
	@echo "  build-upstream   - Build the upstream service binary"
	@echo "  build-both       - Build both binaries"
	@echo "  build-all        - Build for multiple platforms"
	@echo ""
	@echo "Run targets:"
	@echo "  run              - Run the gateway application"
	@echo "  run-config       - Run gateway with test config file"
	@echo "  run-upstream     - Run upstream service (Zone 001)"
	@echo "  run-upstream-zone- Run upstream for specific zone (ZONE=001 PORT=9001)"
	@echo "  run-dev          - Run development environment (gateway + 3 zones)"
	@echo ""
	@echo "Development targets:"
	@echo "  test             - Run tests"
	@echo "  test-cover       - Run tests with coverage"
	@echo "  bench            - Run benchmark tests"
	@echo "  fmt              - Format code"
	@echo "  vet              - Vet code"
	@echo "  lint             - Lint code (requires golangci-lint)"
	@echo "  proto            - Generate protobuf files"
	@echo ""
	@echo "Setup targets:"
	@echo "  deps             - Install dependencies"
	@echo "  tools            - Install development tools"
	@echo "  clean            - Clean build artifacts"
	@echo "  setup            - Setup development environment"
	@echo "  certs            - Generate development certificates"
	@echo ""
	@echo "Install targets:"
	@echo "  install          - Install gateway binary to GOPATH/bin"
	@echo "  install-upstream - Install upstream binary to GOPATH/bin"
	@echo "  install-both     - Install both binaries"
	@echo "  uninstall        - Remove gateway binary from GOPATH/bin"
	@echo "  uninstall-upstream - Remove upstream binary from GOPATH/bin"
	@echo "  uninstall-both   - Remove both binaries"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker           - Build Docker image"
	@echo "  docker-run       - Run Docker container"
	@echo ""
	@echo "Zone management:"
	@echo "  start-zone-001   - Start upstream service for Zone 001"
	@echo "  start-zone-002   - Start upstream service for Zone 002"
	@echo "  start-zone-003   - Start upstream service for Zone 003"
	@echo "  start-zone-004   - Start upstream service for Zone 004"
	@echo "  start-zone-005   - Start upstream service for Zone 005"
	@echo "  start-zone-006   - Start upstream service for Zone 006"
	@echo ""
	@echo "Health & Debugging:"
	@echo "  health-check     - Check health of running services"
	@echo "  quick-start      - Quick build and start (gateway + zone 001)"
	@echo ""
	@echo "Other targets:"
	@echo "  version          - Show version info"
	@echo "  help             - Show this help"

# Zone management helpers
.PHONY: start-zone-001
start-zone-001:
	@echo "Starting upstream service for Zone 001..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=001 --addr=:9001 --gateway=localhost:8092

.PHONY: start-zone-002
start-zone-002:
	@echo "Starting upstream service for Zone 002..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=002 --addr=:9002 --gateway=localhost:8092

.PHONY: start-zone-003
start-zone-003:
	@echo "Starting upstream service for Zone 003..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=003 --addr=:9003 --gateway=localhost:8092

.PHONY: start-zone-004
start-zone-004:
	@echo "Starting upstream service for Zone 004..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=004 --addr=:9004 --gateway=localhost:8092

.PHONY: start-zone-005
start-zone-005:
	@echo "Starting upstream service for Zone 005..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=005 --addr=:9005 --gateway=localhost:8092

.PHONY: start-zone-006
start-zone-006:
	@echo "Starting upstream service for Zone 006..."
	@go run ./$(UPSTREAM_CMD_DIR) --zone=006 --addr=:9006 --gateway=localhost:8092

# Health check for services
.PHONY: health-check
health-check:
	@echo "Checking Gateway health..."
	@curl -f http://localhost:8080/health || echo "Gateway health check failed"
	@echo "Checking Upstream services health..."
	@curl -f http://localhost:10001/health || echo "Zone 001 health check failed"
	@curl -f http://localhost:10002/health || echo "Zone 002 health check failed"
	@curl -f http://localhost:10003/health || echo "Zone 003 health check failed"

# Quick development commands
.PHONY: quick-start
quick-start: build-both
	@echo "Quick start: Building and running gateway + zone 001..."
	@./$(BUILD_DIR)/$(BINARY_NAME) -config=test-config.yaml &
	@sleep 2
	@./$(BUILD_DIR)/$(UPSTREAM_BINARY_NAME) --zone=001 --addr=:9001 --gateway=localhost:8092

# Version info
.PHONY: version
version:
	@echo "$(PROJECT_NAME) $(VERSION)"
	@echo "Go version: $(GO_VERSION)"
	@echo "Build time: $(BUILD_TIME)"