// Exio is the Exio tunneling client CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/sonnytaylor/exio/internal/client"
)

var (
	version = "1.0.0"
	cfgFile string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "exio",
	Short: "Expose local services to the internet",
	Long: `Exio is a high-performance tunneling client that exposes local TCP services
to the public internet via a secure, reverse-proxy architecture.

Examples:
  exio http 3000                    # Expose local port 3000
  exio http 3000 --subdomain myapp  # Request specific subdomain
  exio http 8080 --host 192.168.1.5 # Forward to different host

Configuration via environment variables:
  EXIO_SERVER - Server URL (e.g., https://tunnel.example.com)
  EXIO_TOKEN  - Authentication token`,
}

var httpCmd = &cobra.Command{
	Use:   "http <port>",
	Short: "Expose a local HTTP service",
	Long: `Expose a local HTTP service to the internet through the Exio tunnel.

The local service will be accessible at https://<subdomain>.<base-domain>`,
	Args: cobra.ExactArgs(1),
	RunE: runHTTPTunnel,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.exio.yaml)")
	rootCmd.PersistentFlags().StringP("server", "s", "", "Exio server URL")
	rootCmd.PersistentFlags().StringP("token", "t", "", "Authentication token")

	viper.BindPFlag("server", rootCmd.PersistentFlags().Lookup("server"))
	viper.BindPFlag("token", rootCmd.PersistentFlags().Lookup("token"))

	// HTTP command flags
	httpCmd.Flags().String("subdomain", "", "Request a specific subdomain")
	httpCmd.Flags().String("host", "127.0.0.1", "Local host to forward to")
	httpCmd.Flags().Bool("no-rewrite-host", false, "Don't rewrite the Host header")
	httpCmd.Flags().Bool("tui", false, "Enable interactive TUI for request inspection")

	viper.BindPFlag("subdomain", httpCmd.Flags().Lookup("subdomain"))
	viper.BindPFlag("host", httpCmd.Flags().Lookup("host"))
	viper.BindPFlag("no-rewrite-host", httpCmd.Flags().Lookup("no-rewrite-host"))
	viper.BindPFlag("tui", httpCmd.Flags().Lookup("tui"))

	// Add commands
	rootCmd.AddCommand(httpCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("exio version %s\n", version)
		},
	})
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			viper.AddConfigPath(home)
			viper.SetConfigName(".exio")
		}
		viper.AddConfigPath(".")
	}

	viper.SetEnvPrefix("EXIO")
	viper.AutomaticEnv()

	// Map environment variables
	viper.BindEnv("server", "EXIO_SERVER")
	viper.BindEnv("token", "EXIO_TOKEN")

	viper.ReadInConfig()
}

func runHTTPTunnel(cmd *cobra.Command, args []string) error {
	// Parse port
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %s", args[0])
	}

	serverURL := viper.GetString("server")
	if serverURL == "" {
		return fmt.Errorf("server URL is required (set EXIO_SERVER or use --server)")
	}

	token := viper.GetString("token")
	if token == "" {
		return fmt.Errorf("authentication token is required (set EXIO_TOKEN or use --token)")
	}

	subdomain := viper.GetString("subdomain")
	if subdomain == "" {
		// Generate a random subdomain
		subdomain = generateSubdomain()
	}

	config := &client.Config{
		ServerURL:   serverURL,
		Token:       token,
		Subdomain:   subdomain,
		LocalPort:   port,
		LocalHost:   viper.GetString("host"),
		RewriteHost: !viper.GetBool("no-rewrite-host"),
	}

	// Create client
	c, err := client.New(config)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer c.Close()

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down...")
		cancel()
		c.Close()
	}()

	// Connect
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	// Check if TUI mode is enabled
	useTUI := viper.GetBool("tui")

	if useTUI {
		// Run with interactive TUI
		go c.Run(ctx)
		return client.RunTUI(c)
	}

	// Print connection info (non-TUI mode)
	printConnectionInfo(c)

	// Run and handle traffic
	return c.Run(ctx)
}

func printConnectionInfo(c *client.Client) {
	fmt.Println()
	fmt.Println("╭──────────────────────────────────────────────────────────────╮")
	fmt.Println("│                     Exio Tunnel Active                       │")
	fmt.Println("├──────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Public URL: %-48s│\n", c.PublicURL())
	fmt.Println("│                                                              │")
	fmt.Println("│  Press Ctrl+C to stop the tunnel                             │")
	fmt.Println("╰──────────────────────────────────────────────────────────────╯")
	fmt.Println()
}

// generateSubdomain generates a random subdomain.
func generateSubdomain() string {
	adjectives := []string{"swift", "bright", "calm", "eager", "happy", "quick", "bold", "keen", "warm", "cool"}
	nouns := []string{"fox", "owl", "bear", "wolf", "hawk", "deer", "lynx", "orca", "puma", "seal"}

	// Use current time as a simple seed
	seed := int(os.Getpid())
	adj := adjectives[seed%len(adjectives)]
	noun := nouns[(seed/len(adjectives))%len(nouns)]

	return fmt.Sprintf("%s-%s-%d", adj, noun, seed%1000)
}
