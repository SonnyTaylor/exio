# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Exio?

Exio is a self-hosted tunneling tool for exposing local HTTP and TCP services to the internet. It uses WebSocket transport with Yamux multiplexing, designed to run behind Cloudflare Tunnels for SSL termination and DDoS protection.

Two binaries: `exio` (client CLI) and `exiod` (server daemon).

## Build & Development Commands

```bash
make build          # Build client (bin/exio) and server (bin/exiod)
make build-client   # Build only client
make build-server   # Build only server
make test           # Run tests with race detection and coverage
make test-coverage  # Generate HTML coverage report
make lint           # Run golangci-lint
make build-all      # Cross-compile for Linux/macOS/Windows (amd64, arm64)
make run-server     # Run server in dev mode (port 8080)
make run-client     # Run client in dev mode
```

Run a single test:
```bash
go test -v ./pkg/auth -run TestAuthenticator_Validate
```

## CI Checks

CI runs on Go 1.22/1.23 across Ubuntu, macOS, and Windows. It enforces:
- `gofmt` formatting
- `go vet`
- `staticcheck`
- All tests with race detection

## Architecture

### Package Layout

- **cmd/exio/** — Client CLI (Cobra). Commands: `http`, `tcp`, `version`, `init`
- **cmd/exiod/** — Server CLI (Cobra). Single command with flags for port, token, domain, routing mode
- **pkg/protocol/** — Shared constants, types (`ConnectRequest`, `ConnectResponse`, `RequestLog`), subdomain extraction, path routing utilities
- **pkg/transport/** — WebSocket-to-net.Conn adapter + Yamux session wrapper. Creates multiplexed streams over a single WebSocket
- **pkg/auth/** — PSK Bearer token authentication with constant-time comparison
- **internal/client/** — Client connects via WebSocket, accepts Yamux streams from server, bridges traffic to local service. Includes TUI (Bubbletea) and byte-counting wrappers
- **internal/server/** — HTTP server that upgrades `/_connect` to WebSocket for tunnel registration, then routes incoming HTTP requests to the correct tunnel via Yamux streams. `SessionRegistry` uses `sync.Map` for lock-free reads

### Data Flow

1. Client connects to server at `/_connect` via WebSocket with Bearer token auth
2. Both sides wrap the WebSocket as a `net.Conn` and create a Yamux session
3. Server registers the tunnel in `SessionRegistry` keyed by subdomain
4. When an HTTP request arrives, server looks up the tunnel, opens a Yamux stream, and proxies the request
5. Client accepts the stream, dials the local service, and bridges the connection

### Routing Modes

- **Path-based** (default): URLs like `https://tunnel.example.com/subdomain/path` — subdomain extracted from first path segment
- **Subdomain-based**: URLs like `https://subdomain.tunnel.example.com/path` — requires wildcard SSL cert

### Version Injection

Build-time LDFLAGS inject `main.version`, `main.commit`, and `main.buildTime` into the client and server binaries.

## Code Conventions

- Standard Go conventions: `go fmt`, `go vet`
- Table-driven tests
- Use present tense in commit messages ("Add feature" not "Added feature")
- Config precedence: CLI flags → environment variables → config file (`~/.exio.yaml` / `~/.exiod.yaml`)
