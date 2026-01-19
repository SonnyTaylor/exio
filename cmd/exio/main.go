// Exio is the Exio tunneling client CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/sonnytaylor/exio/internal/client"
	"github.com/sonnytaylor/exio/pkg/protocol"
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

	// Check if TUI mode is enabled
	useTUI := viper.GetBool("tui")

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		if !useTUI {
			// Print a clean shutdown message
			fmt.Println()
			shutdownStyle := lipgloss.NewStyle().Foreground(warningColor)
			fmt.Println(shutdownStyle.Render("   ⏹  Shutting down tunnel..."))
		}
		cancel()
		c.Close()
	}()

	// In non-TUI mode, enable quiet mode and show a connecting spinner
	if !useTUI {
		c.SetQuietMode(true)
		connectingStyle := lipgloss.NewStyle().Foreground(mutedColor).Italic(true)
		fmt.Println()
		fmt.Println(connectingStyle.Render("   Connecting to server..."))
	}

	// Connect
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	if useTUI {
		// Run with interactive TUI
		go c.Run(ctx)
		return client.RunTUI(c)
	}

	// Print connection info (non-TUI mode)
	printConnectionInfo(c)

	// Set up styled request logging callback
	c.OnRequest = func(log protocol.RequestLog) {
		printRequest(log)
	}

	// Run and handle traffic
	return c.Run(ctx)
}

// UI Styles
var (
	// Colors
	primaryColor   = lipgloss.Color("#7C3AED") // Purple
	accentColor    = lipgloss.Color("#10B981") // Green
	mutedColor     = lipgloss.Color("#6B7280") // Gray
	successColor   = lipgloss.Color("#10B981") // Green
	warningColor   = lipgloss.Color("#F59E0B") // Amber
	errorColor     = lipgloss.Color("#EF4444") // Red
	infoColor      = lipgloss.Color("#3B82F6") // Blue

	// Logo/Brand
	logoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor)

	// Main container
	containerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor).
			Padding(1, 2).
			MarginTop(1).
			MarginBottom(1)

	// Title
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(primaryColor).
			Padding(0, 2).
			MarginBottom(1)

	// URL display
	urlLabelStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	urlValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)

	// Status indicator
	statusDotStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	statusTextStyle = lipgloss.NewStyle().
			Foreground(accentColor)

	// Forward info
	forwardStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	// Help text
	helpTextStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			MarginTop(1)

	// Request log styles
	timeStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Width(10)

	methodGetStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(successColor).
			Width(7)

	methodPostStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(warningColor).
			Width(7)

	methodPutStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(infoColor).
			Width(7)

	methodDeleteStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(errorColor).
			Width(7)

	methodPatchStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#8B5CF6")).
			Width(7)

	methodDefaultStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(mutedColor).
			Width(7)

	pathLogStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D1D5DB"))

	statusSuccessStyle = lipgloss.NewStyle().
			Foreground(successColor)

	statusRedirectStyle = lipgloss.NewStyle().
			Foreground(infoColor)

	statusClientErrStyle = lipgloss.NewStyle().
			Foreground(warningColor)

	statusServerErrStyle = lipgloss.NewStyle().
			Foreground(errorColor)

	durationLogStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	arrowStyle = lipgloss.NewStyle().
			Foreground(primaryColor)
)

func printConnectionInfo(c *client.Client) {
	// Logo
	logo := logoStyle.Render(`
   ███████╗██╗  ██╗██╗ ██████╗ 
   ██╔════╝╚██╗██╔╝██║██╔═══██╗
   █████╗   ╚███╔╝ ██║██║   ██║
   ██╔══╝   ██╔██╗ ██║██║   ██║
   ███████╗██╔╝ ██╗██║╚██████╔╝
   ╚══════╝╚═╝  ╚═╝╚═╝ ╚═════╝`)

	fmt.Println(logo)

	// Status line
	statusDot := statusDotStyle.Render("●")
	statusText := statusTextStyle.Render("Tunnel Active")
	statusLine := fmt.Sprintf("   %s %s", statusDot, statusText)
	fmt.Println(statusLine)
	fmt.Println()

	// URL section
	urlLabel := urlLabelStyle.Render("   Public URL")
	fmt.Println(urlLabel)
	urlArrow := arrowStyle.Render("   →")
	urlValue := urlValueStyle.Render(c.PublicURL())
	fmt.Printf("%s %s\n", urlArrow, urlValue)
	fmt.Println()

	// Forward info
	forwardLabel := forwardStyle.Render("   Forwarding to")
	fmt.Println(forwardLabel)
	forwardArrow := arrowStyle.Render("   →")
	localAddr := forwardStyle.Render(fmt.Sprintf("%s:%d", c.Config().LocalHost, c.Config().LocalPort))
	fmt.Printf("%s %s\n", forwardArrow, localAddr)
	fmt.Println()

	// Divider
	divider := lipgloss.NewStyle().Foreground(mutedColor).Render("   " + "─────────────────────────────────────────────────")
	fmt.Println(divider)
	fmt.Println()

	// Help
	helpText := helpTextStyle.Render("   Press Ctrl+C to stop the tunnel")
	fmt.Println(helpText)
	fmt.Println()

	// Request log header
	headerStyle := lipgloss.NewStyle().Foreground(mutedColor).Bold(true)
	fmt.Println(headerStyle.Render("   Requests"))
	fmt.Println()
}

func getMethodStyle(method string) lipgloss.Style {
	switch method {
	case "GET":
		return methodGetStyle
	case "POST":
		return methodPostStyle
	case "PUT":
		return methodPutStyle
	case "DELETE":
		return methodDeleteStyle
	case "PATCH":
		return methodPatchStyle
	default:
		return methodDefaultStyle
	}
}

func getStatusStyle(code int) lipgloss.Style {
	switch {
	case code >= 200 && code < 300:
		return statusSuccessStyle
	case code >= 300 && code < 400:
		return statusRedirectStyle
	case code >= 400 && code < 500:
		return statusClientErrStyle
	case code >= 500:
		return statusServerErrStyle
	default:
		return lipgloss.NewStyle()
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func printRequest(log protocol.RequestLog) {
	timestamp := timeStyle.Render(log.Timestamp.Format("15:04:05"))
	method := getMethodStyle(log.Method).Render(log.Method)
	path := pathLogStyle.Render(log.Path)
	status := getStatusStyle(log.StatusCode).Render(fmt.Sprintf("%d", log.StatusCode))
	duration := durationLogStyle.Render(formatDuration(log.Duration))

	fmt.Printf("   %s  %s %s %s %s\n", timestamp, method, status, duration, path)
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
