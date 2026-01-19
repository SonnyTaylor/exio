// Exiod is the Exio tunneling server daemon.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/sonnytaylor/exio/internal/server"
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
	Use:   "exiod",
	Short: "Exio tunneling server daemon",
	Long: `Exiod is the server component of the Exio tunneling system.

It accepts incoming WebSocket connections from Exio clients and routes
HTTP traffic to the appropriate tunnel based on the Host header subdomain.

Configuration via environment variables:
  EXIO_PORT        - Server listening port (default: 8080)
  EXIO_TOKEN       - Authentication token (required)
  EXIO_BASE_DOMAIN - Base domain for tunnel subdomains (e.g., dev.example.com)`,
	RunE: runServer,
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.exiod.yaml)")
	rootCmd.Flags().IntP("port", "p", 8080, "Server listening port")
	rootCmd.Flags().StringP("token", "t", "", "Authentication token")
	rootCmd.Flags().StringP("domain", "d", "", "Base domain for tunnel URLs")
	rootCmd.Flags().StringP("routing-mode", "r", "path", "Routing mode: 'path' (tunnel.example.com/id/) or 'subdomain' (id.tunnel.example.com)")
	rootCmd.Flags().Int("tcp-port-start", 10000, "Start of TCP port allocation range")
	rootCmd.Flags().Int("tcp-port-end", 20000, "End of TCP port allocation range")
	rootCmd.Flags().Int("rate-limit", 0, "Rate limit per tunnel (requests per minute, 0 = unlimited)")

	viper.BindPFlag("port", rootCmd.Flags().Lookup("port"))
	viper.BindPFlag("token", rootCmd.Flags().Lookup("token"))
	viper.BindPFlag("domain", rootCmd.Flags().Lookup("domain"))
	viper.BindPFlag("routing-mode", rootCmd.Flags().Lookup("routing-mode"))
	viper.BindPFlag("tcp-port-start", rootCmd.Flags().Lookup("tcp-port-start"))
	viper.BindPFlag("tcp-port-end", rootCmd.Flags().Lookup("tcp-port-end"))
	viper.BindPFlag("rate-limit", rootCmd.Flags().Lookup("rate-limit"))

	// Add version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("exiod version %s\n", version)
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
			viper.SetConfigName(".exiod")
		}
		viper.AddConfigPath(".")
	}

	viper.SetEnvPrefix("EXIO")
	viper.AutomaticEnv()

	// Map environment variables
	viper.BindEnv("port", "EXIO_PORT")
	viper.BindEnv("token", "EXIO_TOKEN")
	viper.BindEnv("domain", "EXIO_BASE_DOMAIN")
	viper.BindEnv("routing-mode", "EXIO_ROUTING_MODE")
	viper.BindEnv("tcp-port-start", "EXIO_TCP_PORT_START")
	viper.BindEnv("tcp-port-end", "EXIO_TCP_PORT_END")
	viper.BindEnv("rate-limit", "EXIO_RATE_LIMIT")

	viper.ReadInConfig()
}

func runServer(cmd *cobra.Command, args []string) error {
	routingMode := viper.GetString("routing-mode")
	if routingMode == "" {
		routingMode = "path" // Default to path-based routing
	}

	config := &server.Config{
		Port:         viper.GetInt("port"),
		Token:        viper.GetString("token"),
		BaseDomain:   viper.GetString("domain"),
		RoutingMode:  routingMode,
		TCPPortStart: viper.GetInt("tcp-port-start"),
		TCPPortEnd:   viper.GetInt("tcp-port-end"),
		RateLimit:    viper.GetInt("rate-limit"),
	}

	if config.Token == "" {
		return fmt.Errorf("authentication token is required (set EXIO_TOKEN or use --token)")
	}

	if config.BaseDomain == "" {
		return fmt.Errorf("base domain is required (set EXIO_BASE_DOMAIN or use --domain)")
	}

	if routingMode != "path" && routingMode != "subdomain" {
		return fmt.Errorf("invalid routing mode '%s': must be 'path' or 'subdomain'", routingMode)
	}

	srv, err := server.New(config)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	return srv.Run(context.Background())
}
