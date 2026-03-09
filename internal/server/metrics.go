package server

import (
	"fmt"
	"net/http"
	"time"
)

// handleMetrics serves Prometheus-compatible metrics.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fmt.Fprintf(w, "# HELP exio_up Whether the exio server is up.\n")
	fmt.Fprintf(w, "# TYPE exio_up gauge\n")
	fmt.Fprintf(w, "exio_up 1\n")

	fmt.Fprintf(w, "# HELP exio_uptime_seconds Server uptime in seconds.\n")
	fmt.Fprintf(w, "# TYPE exio_uptime_seconds gauge\n")
	fmt.Fprintf(w, "exio_uptime_seconds %.2f\n", time.Since(s.startedAt).Seconds())

	fmt.Fprintf(w, "# HELP exio_active_tunnels Number of active tunnel connections.\n")
	fmt.Fprintf(w, "# TYPE exio_active_tunnels gauge\n")
	fmt.Fprintf(w, "exio_active_tunnels %d\n", s.registry.Count())

	fmt.Fprintf(w, "# HELP exio_total_requests Total number of proxied requests.\n")
	fmt.Fprintf(w, "# TYPE exio_total_requests counter\n")
	fmt.Fprintf(w, "exio_total_requests %d\n", s.totalRequests.Load())

	// Per-tunnel request counts
	s.registry.ForEach(func(subdomain string, entry *SessionEntry) bool {
		fmt.Fprintf(w, "# HELP exio_tunnel_requests Total requests per tunnel.\n")
		fmt.Fprintf(w, "# TYPE exio_tunnel_requests counter\n")
		fmt.Fprintf(w, "exio_tunnel_requests{subdomain=%q} %d\n", subdomain, entry.RequestCount.Load())
		return true
	})
}
