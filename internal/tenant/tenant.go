package tenant

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Tenant represents an ISP or box owner.
type Tenant struct {
	ID        string
	Name      string
	Subdomain string
}

type contextKey string

const tenantContextKey = contextKey("tenant")

var ErrTenantNotFound = errors.New("tenant not found")

// Middleware inspects the Host header, looks up the tenant, and adds it to the context.
func Middleware(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Strip port if present (e.g., "superfast.hotspot.local:8080" -> "superfast.hotspot.local")
			host := r.Host
			if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
				host = host[:colonIdx]
			}

			// Extract subdomain (e.g., "superfast.hotspot.local" -> "superfast")
			parts := strings.Split(host, ".")
			subdomain := parts[0]

			// Query the database for this subdomain
			var t Tenant
			err := db.QueryRow(r.Context(),
				"SELECT id::text, name, subdomain FROM tenants WHERE subdomain = $1",
				subdomain,
			).Scan(&t.ID, &t.Name, &t.Subdomain)

			if err != nil {
				slog.Warn("tenant lookup failed", "host", r.Host, "subdomain", subdomain, "err", err)
				http.Error(w, "Portal not found. Please check your connection.", http.StatusNotFound)
				return
			}

			slog.Info("tenant resolved", "subdomain", subdomain, "tenant_id", t.ID)
			ctx := context.WithValue(r.Context(), tenantContextKey, &t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext retrieves the Tenant from the request context.
func FromContext(ctx context.Context) *Tenant {
	t, ok := ctx.Value(tenantContextKey).(*Tenant)
	if !ok {
		panic("tenant not found in context")
	}
	return t
}
