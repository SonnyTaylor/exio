# Exio

**High-performance, self-hosted tunneling protocol for exposing local services to the internet.**

Exio is a developer tool that creates secure tunnels from your local machine to a public server, allowing you to expose local HTTP services to the internet. It's designed to work seamlessly behind Cloudflare Tunnels for DDoS protection and SSL termination.

## Features

- **WebSocket-based transport** - Reliably traverses NATs, firewalls, and proxies
- **Yamux multiplexing** - Multiple concurrent HTTP requests over a single connection
- **Automatic Host rewriting** - Works with development servers that have DNS rebinding protection
- **PSK authentication** - Simple shared-secret authentication model
- **Cloudflare-ready** - Designed to sit behind Cloudflare Tunnel for production deployments

## Quick Start

### Client Usage

```bash
# Expose local port 3000 to the internet
exio http 3000

# Request a specific subdomain
exio http 3000 --subdomain my-app

# Forward to a different local host
exio http 8080 --host 192.168.1.100
```

### Environment Configuration

```bash
# Client configuration
export EXIO_SERVER=https://tunnel.example.com
export EXIO_TOKEN=your-secret-token

# Server configuration  
export EXIO_PORT=8080
export EXIO_TOKEN=your-secret-token
export EXIO_BASE_DOMAIN=dev.example.com
```

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/sonnytaylor/exio.git
cd exio

# Build client
go build -o exio ./cmd/exio

# Build server
go build -o exiod ./cmd/exiod
```

### Pre-built Binaries

Download from the [Releases](https://github.com/sonnytaylor/exio/releases) page.

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

### Using systemd (Linux)

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

1. Create a Cloudflare Tunnel in your Zero Trust dashboard
2. Configure public hostname: `*.dev.example.com`
3. Set service: `http://localhost:8080`
4. The tunnel handles SSL termination; Exio receives plain HTTP

## Protocol Details

### Control Plane

The client establishes a WebSocket connection to `/_connect` with:
- `Authorization: Bearer <token>` header for authentication
- `?subdomain=<name>` query parameter to request a subdomain

### Data Plane

1. Server receives HTTP request for `<subdomain>.dev.example.com`
2. Server extracts subdomain from Host header
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

### Server (exiod)

| Flag | Environment | Description |
|------|-------------|-------------|
| `--port, -p` | `EXIO_PORT` | Listening port (default: 8080) |
| `--token, -t` | `EXIO_TOKEN` | Authentication token (required) |
| `--domain, -d` | `EXIO_BASE_DOMAIN` | Base domain for subdomains (required) |

## License

MIT License - see [LICENSE](LICENSE) for details.
