package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter sets up all HTTP routes and middleware.
func NewRouter(db *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()

	// Public routes (No tenant required)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Tenant routes (Require a valid subdomain)
	tenantMux := http.NewServeMux()
	tenantMux.HandleFunc("/", handlePortalHome)

	// Wrap in tenant middleware if DB is available (nil-safe for unit tests)
	if db != nil {
		mux.Handle("/", tenant.Middleware(db)(tenantMux))
	} else {
		mux.Handle("/", tenantMux)
	}

	return mux
}

func handlePortalHome(w http.ResponseWriter, r *http.Request) {
	// Pull the tenant out of the context (injected by middleware)
	t := tenant.FromContext(r.Context())

	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte("<h1>Welcome to " + t.Name + " WiFi</h1><p>Buy a package below.</p>"))
}
