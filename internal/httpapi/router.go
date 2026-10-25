// internal/httpapi/router.go
package httpapi

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed templates/portal.html
var templateFS embed.FS

var portalTemplate = template.Must(template.ParseFS(templateFS, "templates/portal.html"))

type portalViewData struct {
	Tenant *tenant.Tenant
	Plans  []plan.Plan
}

// NewRouter sets up all HTTP routes and middleware.
func NewRouter(db *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()

	// Public health route
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Tenant-scoped portal routes
	tenantMux := http.NewServeMux()
	tenantMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handlePortalHome(w, r, db)
	})

	if db != nil {
		mux.Handle("/", tenant.Middleware(db)(tenantMux))
	} else {
		mux.Handle("/", tenantMux)
	}

	return mux
}

func handlePortalHome(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	var plans []plan.Plan
	if db != nil {
		var err error
		plans, err = plan.ListByTenant(r.Context(), db, t.ID)
		if err != nil {
			slog.Error("failed to list tenant plans", "tenant_id", t.ID, "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := portalTemplate.Execute(w, portalViewData{Tenant: t, Plans: plans}); err != nil {
		slog.Error("template render failed", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
