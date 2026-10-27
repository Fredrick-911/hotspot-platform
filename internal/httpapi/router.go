package httpapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Fredrick-911/hotspot-platform/internal/config"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
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

func NewRouter(db *pgxpool.Pool, cfg config.Config) http.Handler {
	mux := http.NewServeMux()

	// 1. Public health route
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// 2. Public PayHero Webhook Callback (No tenant wrapper needed since external_reference has tenant)
	mux.HandleFunc("/api/payments/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handlePaymentCallback(w, r, db)
	})

	// Tenant-scoped routes
	tenantMux := http.NewServeMux()

	// Device registration API
	tenantMux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		handleDevices(w, r, db)
	})

	// Payment initiation and polling
	tenantMux.HandleFunc("/api/payments/initiate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleInitiatePayment(w, r, db, cfg)
	})

	tenantMux.HandleFunc("/api/payments/status", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		handleCheckPaymentStatus(w, r, db, id)
	})

	// Captive portal UI
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

type registerDeviceReq struct {
	MacAddress   string  `json:"mac_address"`
	NasIPAddress *string `json:"nas_ip_address"`
}

func handleDevices(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	switch r.Method {
	case http.MethodPost:
		var req registerDeviceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request payload"}`, http.StatusBadRequest)
			return
		}

		dev, err := device.Register(r.Context(), db, t.ID, req.MacAddress, req.NasIPAddress)
		if err != nil {
			slog.Error("failed to register device", "tenant_id", t.ID, "err", err)
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		// Strip port from r.Host so MikroTik receives a clean IP or domain
		host := r.Host
		if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
			host = host[:colonIdx]
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device": dev,
			"script": dev.GenerateRouterOSScript(host),
		})

	case http.MethodGet:
		devices, err := device.ListByTenant(r.Context(), db, t.ID)
		if err != nil {
			slog.Error("failed to list devices", "tenant_id", t.ID, "err", err)
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"devices": devices})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
