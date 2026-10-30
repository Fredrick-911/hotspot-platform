// Package httpapi is the web layer: the customer portal, the three dashboards
// (admin, box owner, customer) and the JSON APIs behind them.
package httpapi

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/config"
	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/events"
	"github.com/Fredrick-911/hotspot-platform/internal/radius"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Cookie names.
const (
	cookieStaff    = "hp_session" // admin and box owner
	cookieCustomer = "hp_cust"    // a customer looking at their own pass
)

const (
	staffTTL    = 12 * time.Hour
	customerTTL = 7 * 24 * time.Hour
)

// Server holds everything the handlers share.
type Server struct {
	db     *pgxpool.Pool
	cfg    config.Config
	signer *auth.Signer
	store  *dashboard.Store
	hub    *events.Hub
	pages  *template.Template

	disconnector session.Disconnector

	loginLimiter   *auth.Limiter // staff logins, per IP+email
	loginIPLimiter *auth.Limiter // staff logins, per IP
	custLimiter    *auth.Limiter // customer phone+PIN guesses, per IP and per phone
	payLimiter     *auth.Limiter // STK pushes, per IP and per phone

	checkMu     sync.Mutex
	lastChecked map[string]time.Time // PayHero look-ups per payment, to avoid hammering their API
}

// NewRouter builds the whole web application. db may be nil in unit tests that
// only exercise routes which never touch the database (/health).
func NewRouter(db *pgxpool.Pool, cfg config.Config) http.Handler {
	return NewServer(db, cfg).Handler()
}

// NewServer wires the dependencies.
func NewServer(db *pgxpool.Pool, cfg config.Config) *Server {
	store, err := dashboard.NewStore(db, cfg.Timezone)
	if err != nil { // an unknown APP_TIMEZONE is a deployment mistake: fall back loudly rather than refuse to start
		store, _ = dashboard.NewStore(db, "UTC")
	}
	return &Server{
		db:     db,
		cfg:    cfg,
		signer: auth.NewSigner(cfg.SessionSecret),
		store:  store,
		hub:    events.NewHub(),
		pages:  template.Must(template.ParseFS(templateFS, "templates/*.html")),

		disconnector: session.RadiusDisconnector{DB: db, Client: radius.Client{}},

		loginLimiter:   auth.NewLimiter(5, 15*time.Minute),
		loginIPLimiter: auth.NewLimiter(30, 15*time.Minute),
		custLimiter:    auth.NewLimiter(8, 15*time.Minute),
		payLimiter:     auth.NewLimiter(6, 10*time.Minute),

		lastChecked: map[string]time.Time{},
	}
}

// Hub exposes the event hub (used by tests and the demo seeder).
func (s *Server) Hub() *events.Hub { return s.hub }

// Handler returns the full routing table wrapped in the standard middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Operations.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", s.handleReady)

	// Static files (CSS, JS, icon): embedded in the binary.
	sub, _ := fs.Sub(staticFS, "static")
	static := http.StripPrefix("/static/", http.FileServerFS(sub))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		static.ServeHTTP(w, r)
	}))

	// Router and payment-provider calls (no cookies).
	mux.HandleFunc("POST /api/payments/callback", s.handlePaymentCallback)
	mux.HandleFunc("POST /api/device/heartbeat", s.handleHeartbeat)

	// Staff: login and pages.
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireStaff("", s.handleMe))
	mux.HandleFunc("POST /api/auth/password", s.requireStaff("", s.handleChangePassword))
	mux.HandleFunc("GET /admin", s.requirePage(auth.RoleAdmin, s.handleAdminPage))
	mux.HandleFunc("GET /dashboard", s.requirePage(auth.RoleOwner, s.handleTenantPage))

	// Platform admin API.
	admin := func(h staffHandler) http.HandlerFunc { return s.requireStaff(auth.RoleAdmin, h) }
	mux.HandleFunc("GET /api/admin/overview", admin(s.adminOverview))
	mux.HandleFunc("GET /api/admin/tenants", admin(s.adminTenants))
	mux.HandleFunc("POST /api/admin/tenants", admin(s.adminCreateTenant))
	mux.HandleFunc("PATCH /api/admin/tenants/{id}", admin(s.adminUpdateTenant))
	mux.HandleFunc("POST /api/admin/tenants/{id}/reset-password", admin(s.adminResetPassword))
	mux.HandleFunc("GET /api/admin/payments", admin(s.adminPayments))
	mux.HandleFunc("GET /api/admin/devices", admin(s.adminDevices))
	mux.HandleFunc("GET /api/admin/stream", admin(s.adminStream))

	// Box owner API (always scoped to the tenant on the logged-in user).
	owner := func(h staffHandler) http.HandlerFunc { return s.requireStaff(auth.RoleOwner, h) }
	mux.HandleFunc("GET /api/tenant/overview", owner(s.tenantOverview))
	mux.HandleFunc("GET /api/tenant/payments", owner(s.tenantPayments))
	mux.HandleFunc("GET /api/tenant/customers", owner(s.tenantCustomers))
	mux.HandleFunc("POST /api/tenant/customers/{id}/revoke", owner(s.tenantRevoke))
	mux.HandleFunc("GET /api/tenant/plans", owner(s.tenantPlans))
	mux.HandleFunc("POST /api/tenant/plans", owner(s.tenantCreatePlan))
	mux.HandleFunc("PUT /api/tenant/plans/{id}", owner(s.tenantUpdatePlan))
	mux.HandleFunc("POST /api/tenant/plans/{id}/archive", owner(s.tenantArchivePlan(false)))
	mux.HandleFunc("POST /api/tenant/plans/{id}/restore", owner(s.tenantArchivePlan(true)))
	mux.HandleFunc("GET /api/tenant/devices", owner(s.tenantDevices))
	mux.HandleFunc("POST /api/tenant/devices", owner(s.tenantCreateDevice))
	mux.HandleFunc("PUT /api/tenant/devices/{id}", owner(s.tenantUpdateDevice))
	mux.HandleFunc("DELETE /api/tenant/devices/{id}", owner(s.tenantDeleteDevice))
	mux.HandleFunc("GET /api/tenant/devices/{id}/script", owner(s.tenantDeviceScript))
	mux.HandleFunc("GET /api/tenant/announcements", owner(s.tenantAnnouncements))
	mux.HandleFunc("POST /api/tenant/announcements", owner(s.tenantCreateAnnouncement))
	mux.HandleFunc("DELETE /api/tenant/announcements/{id}", owner(s.tenantDeleteAnnouncement))
	mux.HandleFunc("GET /api/tenant/stream", owner(s.tenantStream))

	// Customer side: everything below belongs to ONE tenant, found from the host name.
	if s.db != nil {
		tm := tenant.Middleware(s.db, s.cfg.AllowTenantQuery)
		mux.Handle("GET /{$}", s.rootHandler(tm(http.HandlerFunc(s.handlePortal))))
		mux.Handle("GET /account", tm(http.HandlerFunc(s.handleAccountPage)))
		mux.Handle("POST /api/payments/initiate", tm(http.HandlerFunc(s.handleInitiatePayment)))
		mux.Handle("GET /api/payments/status", tm(http.HandlerFunc(s.handlePaymentStatus)))
		mux.Handle("POST /api/dev/confirm", tm(http.HandlerFunc(s.handleDevConfirm)))
		mux.Handle("POST /api/sessions/reconnect", tm(http.HandlerFunc(s.handleReconnect)))
		mux.Handle("POST /api/customer/login", tm(http.HandlerFunc(s.handleCustomerLogin)))
		mux.Handle("POST /api/customer/logout", http.HandlerFunc(s.handleCustomerLogout))
		mux.Handle("GET /api/customer/me", tm(http.HandlerFunc(s.handleCustomerMe)))
		mux.Handle("GET /api/portal/announcements", tm(http.HandlerFunc(s.handlePortalAnnouncements)))
		mux.Handle("GET /api/portal/stream", tm(http.HandlerFunc(s.handlePortalStream)))
	}

	return s.chain(mux)
}

// rootHandler sends a visit to the bare server address (no tenant named) to the
// staff login instead of a confusing "portal not found".
func (s *Server) rootHandler(portal http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tenant") == "" && hostOnly(r.Host) == s.cfg.BaseHost() {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		portal.ServeHTTP(w, r)
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no database"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// render executes a named page template.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.pages.ExecuteTemplate(w, name, data); err != nil {
		serverError(w, "render "+name, err)
	}
}
