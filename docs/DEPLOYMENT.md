# Exio Deployment Guide

This guide covers deploying Exio Server in production environments.

## Prerequisites

- Linux server (Debian 12 / Ubuntu 22.04 recommended)
- Cloudflare account with Zero Trust access
- Domain configured in Cloudflare

## Server Setup

### 1. Create System User

```bash
sudo useradd -r -s /bin/false -d /var/lib/exio exio
sudo mkdir -p /var/lib/exio
sudo chown exio:exio /var/lib/exio
```

### 2. Install Binary

Download the latest release or build from source:

```bash
# Download pre-built binary
curl -L https://github.com/SonnyTaylor/exio/releases/latest/download/exiod-linux-amd64 -o exiod
chmod +x exiod
sudo mv exiod /usr/local/bin/
```

Or build from source:

```bash
git clone https://github.com/SonnyTaylor/exio.git
cd exio
make build
sudo cp bin/exiod /usr/local/bin/
```

### 3. Configure Environment

Create configuration directory and file:

```bash
sudo mkdir -p /etc/exio
sudo tee /etc/exio/exiod.env << 'EOF'
# Server listening port
EXIO_PORT=8080

# Authentication token (generate a strong random token)
# openssl rand -hex 32
EXIO_TOKEN=your-secret-token-here

# Base domain for tunnel URLs
EXIO_BASE_DOMAIN=tunnel.example.com

# Routing mode: "path" (default) or "subdomain"
# - path: URLs like https://tunnel.example.com/my-app/
# - subdomain: URLs like https://my-app.tunnel.example.com
EXIO_ROUTING_MODE=path
EOF

# Secure the config file
sudo chmod 600 /etc/exio/exiod.env
sudo chown root:exio /etc/exio/exiod.env
```

### 4. Install systemd Service

```bash
sudo tee /etc/systemd/system/exiod.service << 'EOF'
[Unit]
Description=Exio Tunneling Server Daemon
Documentation=https://github.com/SonnyTaylor/exio
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=exio
Group=exio
EnvironmentFile=/etc/exio/exiod.env
ExecStart=/usr/local/bin/exiod
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes

[Install]
WantedBy=multi-user.target
EOF
```

### 5. Start the Service

```bash
sudo systemctl daemon-reload
sudo systemctl enable exiod
sudo systemctl start exiod

# Check status
sudo systemctl status exiod
sudo journalctl -u exiod -f
```

## Cloudflare Tunnel Setup

### 1. Install cloudflared

```bash
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o cloudflared
chmod +x cloudflared
sudo mv cloudflared /usr/local/bin/
```

### 2. Authenticate

```bash
cloudflared tunnel login
```

### 3. Create Tunnel

```bash
cloudflared tunnel create exio
```

### 4. Configure Tunnel

Create `/etc/cloudflared/config.yml`:

**For path-based routing (recommended):**

```yaml
tunnel: <TUNNEL_ID>
credentials-file: /etc/cloudflared/<TUNNEL_ID>.json

ingress:
  # Single hostname for path-based routing
  - hostname: "tunnel.example.com"
    service: http://localhost:8080
  # Catch-all (required)
  - service: http_status:404
```

**For subdomain-based routing:**

```yaml
tunnel: <TUNNEL_ID>
credentials-file: /etc/cloudflared/<TUNNEL_ID>.json

ingress:
  # Wildcard for all subdomains
  - hostname: "*.tunnel.example.com"
    service: http://localhost:8080
  # Base domain for control plane
  - hostname: "tunnel.example.com"
    service: http://localhost:8080
  # Catch-all (required)
  - service: http_status:404
```

### 5. Configure DNS

In Cloudflare Dashboard:

**For path-based routing (recommended):**
1. Go to DNS settings
2. Add CNAME record:
   - Name: `tunnel`
   - Target: `<TUNNEL_ID>.cfargotunnel.com`
   - Proxy status: Proxied

This is covered by Cloudflare's free Universal SSL certificate.

**For subdomain-based routing:**
1. Go to DNS settings
2. Add CNAME records:
   - Name: `tunnel`, Target: `<TUNNEL_ID>.cfargotunnel.com`, Proxied
   - Name: `*.tunnel`, Target: `<TUNNEL_ID>.cfargotunnel.com`, Proxied
3. Enable Advanced Certificate Manager in SSL/TLS settings to get a certificate for `*.tunnel.example.com`

### 6. Run cloudflared as Service

```bash
sudo cloudflared service install
sudo systemctl start cloudflared
sudo systemctl enable cloudflared
```

## Firewall Configuration

Only cloudflared needs to reach the Exio server. No inbound ports required:

```bash
# If using ufw
sudo ufw default deny incoming
sudo ufw allow ssh
sudo ufw enable

# Exio listens on localhost only via Cloudflare Tunnel
# No public ports needed
```

## Monitoring

### Logs

```bash
# Exio server logs
sudo journalctl -u exiod -f

# Cloudflare tunnel logs
sudo journalctl -u cloudflared -f
```

### Health Check

Create a simple health check script:

```bash
#!/bin/bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health
```

## Scaling Considerations

### Single Server

For most use cases, a single server handles:
- 1000+ concurrent tunnels
- 10,000+ requests/second
- Minimal resource usage

### High Availability

For HA deployments:
1. Run multiple Exio servers behind load balancer
2. Use Redis/etcd for shared session registry
3. Configure Cloudflare load balancing

## Troubleshooting

### Connection Refused

```bash
# Check if exiod is running
sudo systemctl status exiod

# Check port binding
sudo ss -tlnp | grep 8080
```

### Authentication Failures

```bash
# Verify token matches between client and server
echo $EXIO_TOKEN

# Check server logs for auth errors
sudo journalctl -u exiod | grep -i auth
```

### Tunnel Not Found

```bash
# Check if tunnel is registered
# Look for "Tunnel established" in server logs
sudo journalctl -u exiod | grep -i established
```

### Cloudflare 502 Errors

```bash
# Verify cloudflared can reach exiod
curl -v http://localhost:8080/

# Check cloudflared logs
sudo journalctl -u cloudflared | tail -50
```

## Backup and Recovery

### Configuration Backup

```bash
# Backup config
sudo tar -czf exio-config-backup.tar.gz /etc/exio /etc/cloudflared

# Backup credentials
sudo cp /etc/cloudflared/*.json ~/cloudflared-creds-backup/
```

### Recovery

```bash
# Restore config
sudo tar -xzf exio-config-backup.tar.gz -C /

# Restart services
sudo systemctl restart exiod cloudflared
```

## Security Best Practices

1. **Rotate tokens regularly** - Update `EXIO_TOKEN` periodically
2. **Use strong tokens** - Generate with `openssl rand -hex 32`
3. **Limit access** - Only share tokens with authorized developers
4. **Monitor logs** - Watch for suspicious activity
5. **Keep updated** - Regularly update Exio and cloudflared
6. **Use Cloudflare WAF** - Enable additional protection rules
