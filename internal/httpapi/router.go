// Package httpapi wires HTTP routes to handlers. Feature packages (tenant,
// plan, payment, ...) register their routes here as they are built.
package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewRouter returns the HTTP handler for the whole API.
func NewRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	return mux
}

// handleHealth lets load balancers, uptime monitors and CI check the API is alive.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
