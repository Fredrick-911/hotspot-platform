package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values.
const (
	StatusActive    = "ACTIVE"
	StatusSuspended = "SUSPENDED"
)

// Tenant represents an ISP or box owner.
type Tenant struct {
	ID            string
	Name          string
	Subdomain     string
	Status        string
	CommissionBPS int // platform fee in basis points: 1000 = 10.00 %
}

type contextKey string

const tenantContextKey = contextKey("tenant")

var (
	ErrTenantNotFound  = errors.New("tenant not found")
	ErrSubdomainTaken  = errors.New("that subdomain is already taken")
	ErrBadSubdomain    = errors.New("subdomain must be 2-30 characters: a-z, 0-9 and '-', starting with a letter or digit")
	ErrReservedName    = errors.New("that subdomain is reserved")
	ErrBadCommission   = errors.New("commission must be between 0% and 100%")
	ErrBadTenantName   = errors.New("business name must be 2-100 characters")
	subdomainRE        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,29}$`)
	reservedSubdomains = map[string]bool{
		"www": true, "api": true, "admin": true, "app": true, "portal": true, "dashboard": true,
		"static": true, "login": true, "mail": true, "ftp": true, "radius": true, "vpn": true,
		"wg": true, "ns1": true, "ns2": true, "status": true, "support": true, "help": true,
	}
)

// ValidSubdomain checks and normalises a subdomain.
func ValidSubdomain(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if !subdomainRE.MatchString(s) || strings.HasSuffix(s, "-") {
		return "", ErrBadSubdomain
	}
	if reservedSubdomains[s] {
		return "", ErrReservedName
	}
	return s, nil
}

// Create adds a tenant. It is used inside a transaction together with the
// owner's login, so it takes the transaction as its db.
func Create(ctx context.Context, db interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, name, subdomain string, commissionBPS int) (*Tenant, error) {
	name = strings.TrimSpace(name)
	if len(name) < 2 || len(name) > 100 {
		return nil, ErrBadTenantName
	}
	sub, err := ValidSubdomain(subdomain)
	if err != nil {
		return nil, err
	}
	if commissionBPS < 0 || commissionBPS > 10000 {
		return nil, ErrBadCommission
	}
	t := Tenant{Name: name, Subdomain: sub, Status: StatusActive, CommissionBPS: commissionBPS}
	err = db.QueryRow(ctx, `
		INSERT INTO tenants (name, subdomain, commission_bps) VALUES ($1, $2, $3)
		RETURNING id::text`, name, sub, commissionBPS).Scan(&t.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrSubdomainTaken
		}
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	return &t, nil
}

// ByID loads one tenant.
func ByID(ctx context.Context, db *pgxpool.Pool, id string) (*Tenant, error) {
	var t Tenant
	err := db.QueryRow(ctx,
		`SELECT id::text, name, subdomain, status, commission_bps FROM tenants WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.Subdomain, &t.Status, &t.CommissionBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load tenant: %w", err)
	}
	return &t, nil
}

// Middleware finds the tenant from the Host header (kamau.example.com ->
// "kamau") or, when allowQueryOverride is true, the ?tenant= query parameter
// (handy before wildcard DNS exists, and for browser testing). A suspended
// tenant's portal answers 503.
func Middleware(db *pgxpool.Pool, allowQueryOverride bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var subdomain string

			if q := r.URL.Query().Get("tenant"); q != "" && allowQueryOverride {
				subdomain = strings.ToLower(strings.TrimSpace(q))
			} else {
				host := r.Host
				if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
					host = host[:colonIdx]
				}
				subdomain = strings.Split(host, ".")[0]
			}

			var t Tenant
			err := db.QueryRow(r.Context(),
				"SELECT id::text, name, subdomain, status, commission_bps FROM tenants WHERE subdomain = $1",
				subdomain,
			).Scan(&t.ID, &t.Name, &t.Subdomain, &t.Status, &t.CommissionBPS)
			if err != nil {
				slog.Warn("tenant lookup failed", "host", r.Host, "subdomain", subdomain, "err", err)
				http.Error(w, "Portal not found. Please check your connection.", http.StatusNotFound)
				return
			}
			if t.Status != StatusActive {
				http.Error(w, "This Wi-Fi service is temporarily unavailable.", http.StatusServiceUnavailable)
				return
			}

			ctx := context.WithValue(r.Context(), tenantContextKey, &t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithTenant returns ctx carrying t. Used by the middleware and by tests.
func WithTenant(ctx context.Context, t *Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, t)
}

// FromContext retrieves the Tenant from the request context.
func FromContext(ctx context.Context) *Tenant {
	t, ok := ctx.Value(tenantContextKey).(*Tenant)
	if !ok {
		panic("tenant not found in context")
	}
	return t
}
