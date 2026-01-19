package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	defaultConfigDir   = "/etc/exio"
	defaultEnvFile     = "/etc/exio/exiod.env"
	defaultSystemdPath = "/etc/systemd/system/exiod.service"
	defaultBinaryPath  = "/usr/local/bin/exiod"
	defaultPort        = 8080
	defaultRoutingMode = "path"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Exio server configuration",
	Long: `Interactive setup wizard to configure the Exio server.

This command will:
- Generate a secure authentication token
- Configure the server settings (domain, port, routing mode)
- Create the configuration file at /etc/exio/exiod.env
- Optionally install and enable the systemd service

Run with sudo for full functionality (systemd installation).`,
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("  ╭───────────────────────────────────╮")
	fmt.Println("  │     Exio Server Setup Wizard      │")
	fmt.Println("  │   Configure your tunnel server    │")
	fmt.Println("  ╰───────────────────────────────────╯")
	fmt.Println()

	// Check OS
	if runtime.GOOS == "windows" {
		fmt.Println("Note: Windows detected. Systemd features are not available.")
		fmt.Println("      Configuration will be saved for manual use.")
		fmt.Println()
	}

	// Check for root privileges on Linux/macOS
	isRoot := os.Geteuid() == 0
	if runtime.GOOS != "windows" && !isRoot {
		fmt.Println("Warning: Not running as root. Some features will be limited:")
		fmt.Println("  - Cannot create /etc/exio directory")
		fmt.Println("  - Cannot install systemd service")
		fmt.Println()
		fmt.Println("Run with sudo for full functionality: sudo exiod init")
		fmt.Println()
		fmt.Print("Continue anyway? [y/N]: ")
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	// Check for existing config
	configPath := defaultEnvFile
	if !isRoot {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".exiod.env")
	}

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Existing configuration found at %s\n", configPath)
		fmt.Print("Overwrite? [y/N]: ")
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	// Generate secure token
	fmt.Print("Generating secure authentication token... ")
	token, err := generateSecureToken(32)
	if err != nil {
		return fmt.Errorf("failed to generate token: %w", err)
	}
	fmt.Println("Done")
	fmt.Println()

	// Get base domain
	fmt.Println("Enter the base domain for your tunnel server.")
	fmt.Println("This is the domain where your server will be accessible.")
	fmt.Println("Example: tunnel.example.com")
	fmt.Println()
	fmt.Print("Base domain: ")
	domain, _ := reader.ReadString('\n')
	domain = strings.TrimSpace(domain)

	if domain == "" {
		return fmt.Errorf("base domain is required")
	}

	// Validate domain (basic check)
	if !isValidDomain(domain) {
		return fmt.Errorf("invalid domain format: %s", domain)
	}

	// Get port
	fmt.Println()
	fmt.Printf("Server port [%d]: ", defaultPort)
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)

	port := defaultPort
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("invalid port number: %s", portStr)
		}
		port = p
	}

	// Get routing mode
	fmt.Println()
	fmt.Println("Routing mode:")
	fmt.Println("  1. path      - URLs like https://tunnel.example.com/my-app/")
	fmt.Println("  2. subdomain - URLs like https://my-app.tunnel.example.com")
	fmt.Println()
	fmt.Println("Path mode is recommended (works with free Cloudflare SSL).")
	fmt.Println("Subdomain mode requires wildcard SSL certificate.")
	fmt.Println()
	fmt.Printf("Routing mode [path]: ")
	routingInput, _ := reader.ReadString('\n')
	routingInput = strings.TrimSpace(strings.ToLower(routingInput))

	routingMode := defaultRoutingMode
	switch routingInput {
	case "", "1", "path":
		routingMode = "path"
	case "2", "subdomain":
		routingMode = "subdomain"
	default:
		return fmt.Errorf("invalid routing mode: %s (use 'path' or 'subdomain')", routingInput)
	}

	fmt.Println()

	// Create configuration
	config := ServerConfig{
		Port:        port,
		Token:       token,
		BaseDomain:  domain,
		RoutingMode: routingMode,
	}

	// Save configuration
	fmt.Print("Saving configuration... ")
	if err := saveServerConfig(config, configPath, isRoot); err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Done")
	fmt.Printf("Configuration saved to: %s\n", configPath)

	// Install systemd service (Linux only, root only)
	if runtime.GOOS == "linux" && isRoot {
		fmt.Println()
		fmt.Print("Install systemd service? [Y/n]: ")
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "" || response == "y" || response == "yes" {
			if err := installSystemdService(); err != nil {
				fmt.Printf("\nWarning: Failed to install systemd service: %v\n", err)
				fmt.Println("You can install it manually later.")
			} else {
				fmt.Println()
				fmt.Println("Systemd service installed and enabled.")
				fmt.Println()
				fmt.Print("Start the server now? [Y/n]: ")
				startResp, _ := reader.ReadString('\n')
				startResp = strings.TrimSpace(strings.ToLower(startResp))

				if startResp == "" || startResp == "y" || startResp == "yes" {
					if err := startService(); err != nil {
						fmt.Printf("Warning: Failed to start service: %v\n", err)
					} else {
						fmt.Println("Server started successfully!")
					}
				}
			}
		}
	}

	// Print summary
	printSetupSummary(config, configPath, isRoot)

	return nil
}

// ServerConfig holds the server configuration values.
type ServerConfig struct {
	Port        int
	Token       string
	BaseDomain  string
	RoutingMode string
}

func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func isValidDomain(domain string) bool {
	// Basic domain validation
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}

	// Remove protocol if accidentally included
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")

	// Check for valid characters
	for _, part := range strings.Split(domain, ".") {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		for i, c := range part {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || (c == '-' && i > 0 && i < len(part)-1)) {
				return false
			}
		}
	}

	return strings.Contains(domain, ".")
}

func saveServerConfig(config ServerConfig, path string, isRoot bool) error {
	// Create directory if needed
	dir := filepath.Dir(path)
	if isRoot {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	content := fmt.Sprintf(`# Exio Server Configuration
# Generated by 'exiod init'

# Server listening port
EXIO_PORT=%d

# Authentication token (keep this secret!)
EXIO_TOKEN=%s

# Base domain for tunnel URLs
EXIO_BASE_DOMAIN=%s

# Routing mode: "path" or "subdomain"
EXIO_ROUTING_MODE=%s
`, config.Port, config.Token, config.BaseDomain, config.RoutingMode)

	// Write with restricted permissions
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return err
	}

	return nil
}

func installSystemdService() error {
	fmt.Print("Creating exio system user... ")
	// Create system user (ignore error if exists)
	cmd := exec.Command("useradd", "-r", "-s", "/bin/false", "-d", "/var/lib/exio", "exio")
	cmd.Run() // Ignore error - user might exist
	fmt.Println("Done")

	fmt.Print("Installing systemd service... ")
	if err := os.WriteFile(defaultSystemdPath, []byte(systemdServiceContent), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}
	fmt.Println("Done")

	fmt.Print("Reloading systemd... ")
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	fmt.Println("Done")

	fmt.Print("Enabling exiod service... ")
	if err := exec.Command("systemctl", "enable", "exiod").Run(); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}
	fmt.Println("Done")

	return nil
}

func startService() error {
	return exec.Command("systemctl", "start", "exiod").Run()
}

func printSetupSummary(config ServerConfig, configPath string, isRoot bool) {
	fmt.Println()
	fmt.Println("  ╭───────────────────────────────────────────────────────╮")
	fmt.Println("  │              Setup Complete!                          │")
	fmt.Println("  ╰───────────────────────────────────────────────────────╯")
	fmt.Println()
	fmt.Println("  Server Configuration:")
	fmt.Printf("    Domain:       %s\n", config.BaseDomain)
	fmt.Printf("    Port:         %d\n", config.Port)
	fmt.Printf("    Routing:      %s\n", config.RoutingMode)
	fmt.Printf("    Config file:  %s\n", configPath)
	fmt.Println()
	fmt.Println("  ─────────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("  Client Connection Info (share with users):")
	fmt.Println()

	// Determine the server URL based on routing mode
	serverURL := fmt.Sprintf("https://%s", config.BaseDomain)

	fmt.Printf("    Server URL:   %s\n", serverURL)
	fmt.Printf("    Token:        %s\n", config.Token)
	fmt.Println()
	fmt.Println("  ─────────────────────────────────────────────────────────")
	fmt.Println()

	if runtime.GOOS == "linux" && isRoot {
		fmt.Println("  Server Management:")
		fmt.Println("    sudo systemctl start exiod    # Start server")
		fmt.Println("    sudo systemctl stop exiod     # Stop server")
		fmt.Println("    sudo systemctl status exiod   # Check status")
		fmt.Println("    sudo journalctl -u exiod -f   # View logs")
	} else if runtime.GOOS != "windows" && !isRoot {
		fmt.Println("  To start the server manually:")
		fmt.Printf("    source %s && exiod\n", configPath)
	} else {
		fmt.Println("  To start the server:")
		fmt.Println("    Set the environment variables from the config file")
		fmt.Println("    Then run: exiod")
	}

	fmt.Println()
	fmt.Println("  Next Steps:")
	fmt.Println("    1. Set up Cloudflare Tunnel (see docs/DEPLOYMENT.md)")
	fmt.Println("    2. Share the Server URL and Token with your users")
	fmt.Println("    3. Users run: exio init")
	fmt.Println()
}

// Embedded systemd service content
const systemdServiceContent = `[Unit]
Description=Exio Tunneling Server Daemon
Documentation=https://github.com/sonnytaylor/exio
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=exio
Group=exio

# Environment configuration
EnvironmentFile=-/etc/exio/exiod.env

# The server binary
ExecStart=/usr/local/bin/exiod

# Restart configuration
Restart=on-failure
RestartSec=5s

# Resource limits
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=exiod

[Install]
WantedBy=multi-user.target
`
