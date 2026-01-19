# Exio

**High-performance, self-hosted tunneling protocol for exposing local services to the internet.**

Exio is a developer tool that creates secure tunnels from your local machine to a public server, allowing you to expose local HTTP services to the internet. It's designed to work seamlessly behind Cloudflare Tunnels for DDoS protection and SSL termination.

[![CI](https://github.com/SonnyTaylor/exio/actions/workflows/ci.yml/badge.svg)](https://github.com/SonnyTaylor/exio/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/SonnyTaylor/exio)](https://github.com/SonnyTaylor/exio/releases)
[![License](https://img.shields.io/github/license/SonnyTaylor/exio)](LICENSE)

## Features

- **WebSocket-based transport** - Reliably traverses NATs, firewalls, and proxies
- **Yamux multiplexing** - Multiple concurrent HTTP requests over a single connection
- **Automatic Host rewriting** - Works with development servers that have DNS rebinding protection
- **PSK authentication** - Simple shared-secret authentication model
- **Interactive TUI** - Real-time request inspection with `--tui` flag
- **Cloudflare-ready** - Designed to sit behind Cloudflare Tunnel for production deployments
- **Flexible routing** - Path-based (`tunnel.example.com/id/`) or subdomain-based (`id.tunnel.example.com`) routing

## Installation

### Quick Install (Recommended)

**Linux / macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/SonnyTaylor/exio/main/install.sh | sh
```

**Windows (PowerShell):**
```powershell
irm https://raw.githubusercontent.com/SonnyTaylor/exio/main/install.ps1 | iex
```

### From Source

```bash
git clone https://github.com/SonnyTaylor/exio.git
cd exio
make build
```

### Pre-built Binaries

Download from the [Releases](https://github.com/SonnyTaylor/exio/releases) page.

## Quick Start

### 1. Configure (First Time)

```bash
exio init
```

This interactive wizard will prompt you for your server URL and authentication token, saving the configuration to `~/.exio.yaml`.

### 2. Expose Your Service

```bash
# Expose local port 3000
exio http 3000

# Request a specific tunnel ID
exio http 3000 --subdomain my-app

# With real-time request viewer
exio http 3000 --tui
```

Your service will be available at a URL like:
- **Path mode (default)**: `https://tunnel.example.com/my-app/`
- **Subdomain mode**: `https://my-app.tunnel.example.com`

### Manual Configuration

Alternatively, configure via environment variables:

```bash
# Client configuration
export EXIO_SERVER=https://tunnel.example.com
export EXIO_TOKEN=your-secret-token

# Server configuration  
export EXIO_PORT=8080
export EXIO_TOKEN=your-secret-token
export EXIO_BASE_DOMAIN=dev.example.com
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Public Internet                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Cloudflare Edge Network                       │
│           (SSL Termination, DDoS Protection, WAF)               │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼ (cloudflared tunnel)
┌─────────────────────────────────────────────────────────────────┐
│                      Exio Server (exiod)                         │
│                                                                  │
│  ┌──────────────┐  ┌──────────────────┐  ┌──────────────────┐   │
│  │ Control Plane│  │ Session Registry │  │   Data Plane     │   │
│  │ /_connect    │  │ map[subdomain]   │  │   HTTP Router    │   │
│  │ WebSocket    │──│    *Session      │──│   Host→Tunnel    │   │
│  └──────────────┘  └──────────────────┘  └──────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ WebSocket + Yamux
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Exio Client (exio)                          │
│                                                                  │
│  ┌──────────────┐  ┌──────────────────┐  ┌──────────────────┐   │
│  │  Connection  │  │  Stream Handler  │  │  Local Proxy     │   │
│  │  Manager     │──│  Accept & Bridge │──│  Host Rewrite    │   │
│  └──────────────┘  └──────────────────┘  └──────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Local Development Server                      │
│                    (localhost:3000, etc.)                        │
└─────────────────────────────────────────────────────────────────┘
```

## Server Deployment

### Quick Install (Recommended)

**Linux / macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/SonnyTaylor/exio/main/install-server.sh | sudo sh
```

This will download the server binary and launch the interactive setup wizard.

### Interactive Setup Wizard

If you already have the binary installed, run the setup wizard:

```bash
sudo exiod init
```

The wizard will:
- Generate a secure authentication token
- Configure your domain, port, and routing mode
- Create the configuration file (`/etc/exio/exiod.env`)
- Install and enable the systemd service
- Display the client connection info to share with users

### Manual Setup (Linux systemd)

```bash
# Create exio user
sudo useradd -r -s /bin/false exio

# Install binary
sudo cp exiod /usr/local/bin/
sudo chmod +x /usr/local/bin/exiod

# Create config directory
sudo mkdir -p /etc/exio
sudo cp deploy/exiod.env.example /etc/exio/exiod.env
sudo chmod 600 /etc/exio/exiod.env

# Edit configuration
sudo nano /etc/exio/exiod.env

# Install and start service
sudo cp deploy/exiod.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable exiod
sudo systemctl start exiod
```

### Cloudflare Tunnel Configuration

**For path-based routing (recommended):**
1. Create a Cloudflare Tunnel in your Zero Trust dashboard
2. Configure public hostname: `tunnel.example.com`
3. Set service: `http://localhost:8080`
4. Add a single DNS CNAME record for `tunnel` pointing to your tunnel

**For subdomain-based routing:**
1. Create a Cloudflare Tunnel in your Zero Trust dashboard
2. Configure public hostname: `*.tunnel.example.com`
3. Set service: `http://localhost:8080`
4. Add a wildcard DNS CNAME record for `*.tunnel`
5. Requires Cloudflare Advanced Certificate Manager for SSL on `*.tunnel.example.com`

The tunnel handles SSL termination; Exio receives plain HTTP.

## Protocol Details

### Control Plane

The client establishes a WebSocket connection to `/_connect` with:
- `Authorization: Bearer <token>` header for authentication
- `?subdomain=<name>` query parameter to request a subdomain

### Data Plane

**Path-based routing (default):**
1. Server receives HTTP request for `tunnel.example.com/my-app/api/users`
2. Server extracts tunnel ID (`my-app`) from the first path segment
3. Server rewrites path to `/api/users` (strips tunnel ID prefix)
4. Server looks up session in registry
5. Server opens new Yamux stream to client
6. Server writes modified HTTP request to stream
7. Client reads request, forwards to local service
8. Client writes response back to stream
9. Server copies response to original HTTP response writer

**Subdomain-based routing:**
1. Server receives HTTP request for `my-app.tunnel.example.com/api/users`
2. Server extracts subdomain (`my-app`) from Host header
3. Server looks up session in registry
4. Server opens new Yamux stream to client
5. Server writes raw HTTP request to stream
6. Client reads request, forwards to local service
7. Client writes response back to stream
8. Server copies response to original HTTP response writer

### Host Header Rewriting

By default, the client rewrites the `Host` header to `127.0.0.1:<port>` before forwarding to the local service. This is necessary because many development frameworks (Next.js, Django, Rails) reject requests with unrecognized Host headers as a DNS rebinding protection.

Use `--no-rewrite-host` to disable this behavior if your local service requires the original Host header.

## Configuration Reference

### Client (exio)

| Flag | Environment | Description |
|------|-------------|-------------|
| `--server, -s` | `EXIO_SERVER` | Server URL (required) |
| `--token, -t` | `EXIO_TOKEN` | Authentication token (required) |
| `--subdomain` | - | Requested subdomain |
| `--host` | - | Local host to forward to (default: 127.0.0.1) |
| `--no-rewrite-host` | - | Don't rewrite Host header |
| `--tui` | - | Enable interactive request viewer |

### Client Commands

| Command | Description |
|---------|-------------|
| `exio init` | Interactive setup wizard |
| `exio http <port>` | Expose local HTTP service |
| `exio version` | Show version information |

### Server Commands

| Command | Description |
|---------|-------------|
| `exiod init` | Interactive server setup wizard (generates token, configures systemd) |
| `exiod` | Start the server |
| `exiod version` | Show version information |

### Server (exiod)

| Flag | Environment | Description |
|------|-------------|-------------|
| `--port, -p` | `EXIO_PORT` | Listening port (default: 8080) |
| `--token, -t` | `EXIO_TOKEN` | Authentication token (required) |
| `--domain, -d` | `EXIO_BASE_DOMAIN` | Base domain for tunnel URLs (required) |
| `--routing-mode, -r` | `EXIO_ROUTING_MODE` | Routing mode: `path` (default) or `subdomain` |

### Routing Modes

| Mode | URL Format | SSL Requirements |
|------|------------|------------------|
| `path` | `https://tunnel.example.com/my-app/` | Standard SSL (free Cloudflare) |
| `subdomain` | `https://my-app.tunnel.example.com` | Wildcard SSL (Advanced Certificate Manager) |

## License

MIT License - see [LICENSE](LICENSE) for details.
