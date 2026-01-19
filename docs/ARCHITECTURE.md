# Exio Architecture

This document describes the internal architecture of the Exio tunneling system.

## Overview

Exio follows a Hub-and-Spoke topology where the server acts as a generic TCP router and clients act as edge terminators.

```
                    ┌─────────────────────────┐
                    │   Cloudflare Edge       │
                    │   (SSL, DDoS, WAF)      │
                    └───────────┬─────────────┘
                                │
                    ┌───────────▼─────────────┐
                    │   Exio Server (exiod)   │
                    │   ┌─────────────────┐   │
                    │   │ Session Registry│   │
                    │   │ map[subdomain]  │   │
                    │   │   → Session     │   │
                    │   └─────────────────┘   │
                    └───────────┬─────────────┘
                                │ WebSocket + Yamux
              ┌─────────────────┼─────────────────┐
              │                 │                 │
    ┌─────────▼───────┐ ┌──────▼──────┐ ┌───────▼───────┐
    │  Client A       │ │  Client B   │ │  Client C     │
    │  myapp.dev.ex   │ │  api.dev.ex │ │  test.dev.ex  │
    │  → localhost:3000│ │ → :8080    │ │  → :4000      │
    └─────────────────┘ └─────────────┘ └───────────────┘
```

## Protocol Stack

### Layer 1: WebSocket Transport

WebSockets provide the persistent bidirectional connection between client and server. We use WebSockets because:

1. **Traverses proxies**: Works through corporate proxies and firewalls
2. **Cloudflare compatible**: Reliably works with Cloudflare Tunnels
3. **Persistent**: Single long-lived connection vs repeated HTTP requests

The WebSocket connection is established at `/_connect` with authentication.

### Layer 2: Yamux Multiplexing

[Yamux](https://github.com/hashicorp/yamux) multiplexes multiple logical streams over the single WebSocket connection.

**Why Yamux?**
- Prevents Head-of-Line (HOL) blocking
- Enables concurrent requests (100+ streams)
- TCP-like flow control
- Battle-tested by HashiCorp

**Stream Lifecycle:**
```
1. Server receives HTTP request for subdomain
2. Server opens new Yamux stream to client
3. HTTP request written to stream
4. Client reads, forwards to local service
5. Response written back to stream
6. Server copies response to original HTTP writer
7. Stream closed
```

### Layer 3: HTTP Routing

The server routes requests based on the `Host` header:

```go
// Extract subdomain from "myapp.dev.example.com"
subdomain := extractSubdomain(req.Host, baseDomain)

// Lookup session in registry
session := registry.Get(subdomain)

// Open stream and forward request
stream := session.OpenStream()
req.Write(stream)
```

## Component Details

### Server Components

#### Session Registry

Thread-safe map storing active tunnel sessions:

```go
type SessionRegistry struct {
    sessions sync.Map // map[string]*SessionEntry
    count    atomic.Int64
}
```

Key operations:
- `Register(subdomain, session)`: Add new session
- `Get(subdomain)`: Lookup session for routing
- `Unregister(subdomain)`: Remove on disconnect

#### Control Plane Handler

Handles WebSocket upgrade at `/_connect`:

1. Validates `Authorization: Bearer <token>`
2. Checks subdomain availability
3. Upgrades to WebSocket
4. Creates Yamux server session
5. Registers in session registry

#### Data Plane Handler

Routes incoming HTTP requests:

1. Extracts subdomain from Host header
2. Looks up session in registry
3. Opens Yamux stream
4. Writes HTTP request to stream
5. Reads response from stream
6. Copies to HTTP response writer

### Client Components

#### Connection Manager

Handles server connection with:
- Exponential backoff reconnection
- Authentication header injection
- WebSocket dial configuration

#### Stream Handler

Accepts incoming streams from server:

```go
for {
    stream := session.AcceptStream()
    go handleStream(stream)
}
```

#### Traffic Bridge

For each stream:
1. Read HTTP request
2. Rewrite Host header (optional)
3. Dial local service
4. Forward request
5. Copy response back

### Host Header Rewriting

Many dev servers reject requests with unfamiliar Host headers (DNS rebinding protection). The client optionally rewrites:

```
Before: Host: myapp.dev.example.com
After:  Host: 127.0.0.1:3000
```

Preserves original in `X-Forwarded-Host`.

## Data Flow

### Request Path (Inbound)

```
User Browser
    │
    ▼ HTTPS request to myapp.dev.example.com
Cloudflare Edge
    │
    ▼ HTTP request (SSL terminated)
Exio Server
    │
    ▼ Yamux stream with HTTP payload
Exio Client
    │
    ▼ HTTP request with rewritten Host
Local Dev Server (localhost:3000)
```

### Response Path (Outbound)

```
Local Dev Server
    │
    ▼ HTTP response
Exio Client
    │
    ▼ Yamux stream with HTTP payload
Exio Server
    │
    ▼ HTTP response
Cloudflare Edge
    │
    ▼ HTTPS response
User Browser
```

## Security Model

### Authentication

- Pre-shared key (PSK) model
- Token passed in `Authorization: Bearer <token>` header
- Constant-time comparison to prevent timing attacks

### Transport Security

- TLS termination at Cloudflare Edge
- Traffic encrypted over public internet
- Internal traffic (Cloudflare → Server) is HTTP over secure tunnel

### Isolation

- Server only inspects headers for routing
- Request/response bodies passed through without inspection
- Each subdomain maps to exactly one client session

## Performance Considerations

### Concurrency

- Yamux supports 100+ concurrent streams
- Each stream handled in separate goroutine
- Registry uses `sync.Map` for lock-free reads

### Memory

- Idle client: <20MB RAM
- Per-stream overhead: ~8KB buffers
- Session registry: O(n) where n = active tunnels

### Latency

- Target: <15ms overhead per request
- Dominated by network RTT, not processing
- Zero-copy where possible

## Error Handling

| Scenario | Server Response | Client Action |
|----------|-----------------|---------------|
| Subdomain not found | 404 Not Found | N/A |
| Client disconnected | 502 Bad Gateway | Reconnect |
| Local service down | 502 Bad Gateway | Return error |
| Auth failure | 401 Unauthorized | Show error |
| Subdomain taken | 409 Conflict | Choose new subdomain |

## Future Architecture (V2)

### TCP Tunneling

Bypass HTTP routing for raw TCP:
- Dedicated ports per tunnel
- No Host header parsing
- Support SSH, RDP, databases

### Introspection UI

Web interface at `localhost:4040`:
- Request/response inspection
- Real-time traffic monitoring
- Replay functionality
