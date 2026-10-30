package dashboard

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// TenantRow is one box owner in the admin's Tenants table.
type TenantRow struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Subdomain     string    `json:"subdomain"`
	Status        string    `json:"status"`
	CommissionBPS int       `json:"commission_bps"`
	CreatedAt     time.Time `json:"created_at"`
	OwnerEmail    *string   `json:"owner_email"`
	Plans         int64     `json:"plans"`
	Routers       int64     `json:"routers"`
	RoutersOnline int64     `json:"routers_online"`
	ActivePasses  int64     `json:"active_passes"`
	OnlineNow     int64     `json:"online_now"`
	Gross30d      int64     `json:"gross_30d_cents"`
	Fee30d        int64     `json:"fee_30d_cents"`
	Sales30d      int64     `json:"sales_30d"`
}

// TenantCounts is the admin's headline tenant numbers.
type TenantCounts struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Suspended int64 `json:"suspended"`
}

// AdminOverview is the platform operator's home screen.
type AdminOverview struct {
	PeriodTotals
	Live
	Tenants    TenantCounts `json:"tenants"`
	Daily      []DayPoint   `json:"daily"`
	TopTenants []TenantRow  `json:"top_tenants"`
}

// AdminOverview builds platform-wide numbers.
func (s *Store) AdminOverview(ctx ctxT) (*AdminOverview, error) {
	var (
		o   AdminOverview
		err error
	)
	if o.PeriodTotals, err = s.periodTotals(ctx, ""); err != nil {
		return nil, err
	}
	if o.Live, err = s.live(ctx, ""); err != nil {
		return nil, err
	}
	if o.Daily, err = s.daily(ctx, ""); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'ACTIVE'), COUNT(*) FILTER (WHERE status = 'SUSPENDED')
		FROM tenants`).Scan(&o.Tenants.Total, &o.Tenants.Active, &o.Tenants.Suspended); err != nil {
		return nil, fmt.Errorf("tenant counts: %w", err)
	}
	all, err := s.Tenants(ctx)
	if err != nil {
		return nil, err
	}
	// Top 5 by 30-day gross.
	sort.SliceStable(all, func(i, j int) bool { return all[i].Gross30d > all[j].Gross30d })
	if len(all) > 5 {
		all = all[:5]
	}
	o.TopTenants = all
	return &o, nil
}

// Tenants lists every tenant with its 30-day numbers.
func (s *Store) Tenants(ctx ctxT) ([]TenantRow, error) {
	_, _, month := s.periodStarts()
	rows, err := s.DB.Query(ctx, `
		SELECT t.id::text, t.name, t.subdomain, t.status, t.commission_bps, t.created_at,
		  (SELECT u.email FROM users u WHERE u.tenant_id = t.id AND u.role = 'owner' ORDER BY u.created_at LIMIT 1),
		  (SELECT COUNT(*) FROM plans WHERE tenant_id = t.id AND active),
		  (SELECT COUNT(*) FROM devices WHERE tenant_id = t.id),
		  (SELECT COUNT(*) FROM devices WHERE tenant_id = t.id AND last_seen_at >= NOW() - interval '3 minutes'),
		  (SELECT COUNT(*) FROM sessions WHERE tenant_id = t.id AND status = 'ACTIVE' AND expires_at > NOW()),
		  (SELECT COUNT(DISTINCT a.username) FROM radacct a JOIN sessions s ON s.username = a.username
		     WHERE s.tenant_id = t.id AND a.acctstoptime IS NULL AND COALESCE(a.acctupdatetime, a.acctstarttime) >= `+onlineWindowSQL+`),
		  COALESCE((SELECT SUM(amount_cents) FROM payments WHERE tenant_id = t.id AND status = 'SUCCESS' AND created_at >= $1), 0)::bigint,
		  COALESCE((SELECT SUM(fee_cents) FROM payments WHERE tenant_id = t.id AND status = 'SUCCESS' AND created_at >= $1), 0)::bigint,
		  (SELECT COUNT(*) FROM payments WHERE tenant_id = t.id AND status = 'SUCCESS' AND created_at >= $1)
		FROM tenants t ORDER BY t.created_at DESC`, month)
	if err != nil {
		return nil, fmt.Errorf("tenants: %w", err)
	}
	defer rows.Close()
	out := []TenantRow{}
	for rows.Next() {
		var t TenantRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Subdomain, &t.Status, &t.CommissionBPS, &t.CreatedAt, &t.OwnerEmail,
			&t.Plans, &t.Routers, &t.RoutersOnline, &t.ActivePasses, &t.OnlineNow, &t.Gross30d, &t.Fee30d, &t.Sales30d); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// NewTenant is what the admin submits to onboard a box owner.
type NewTenant struct {
	Name          string `json:"name"`
	Subdomain     string `json:"subdomain"`
	OwnerEmail    string `json:"owner_email"`
	OwnerName     string `json:"owner_name"`
	CommissionBPS int    `json:"commission_bps"`
	SeedPlans     bool   `json:"seed_plans"`
}

// CreatedTenant is returned once: the temporary password is never stored in clear.
type CreatedTenant struct {
	Tenant       TenantRow `json:"tenant"`
	OwnerEmail   string    `json:"owner_email"`
	TempPassword string    `json:"temp_password"`
}

// starterPlans give a new owner something to sell on day one. They can edit or archive them.
var starterPlans = []struct {
	Name    string
	KES     int
	Minutes int
}{
	{"1 Hour", 20, 60},
	{"3 Hours", 50, 180},
	{"1 Day", 100, 1440},
}

// CreateTenant onboards a box owner: the tenant, the owner's login (with a
// generated temporary password) and, optionally, three starter plans.
func (s *Store) CreateTenant(ctx ctxT, in NewTenant) (*CreatedTenant, error) {
	pw, err := auth.GeneratePassword()
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t, err := tenant.Create(ctx, tx, in.Name, in.Subdomain, in.CommissionBPS)
	if err != nil {
		return nil, err
	}
	if _, err := auth.Create(ctx, tx, t.ID, in.OwnerEmail, in.OwnerName, pw, auth.RoleOwner); err != nil {
		return nil, err
	}
	if in.SeedPlans {
		for _, p := range starterPlans {
			if _, err := tx.Exec(ctx, `
				INSERT INTO plans (tenant_id, name, price_cents, duration_minutes) VALUES ($1, $2, $3, $4)`,
				t.ID, p.Name, p.KES*100, p.Minutes); err != nil {
				return nil, fmt.Errorf("starter plans: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	email, _ := auth.NormalizeEmail(in.OwnerEmail)
	return &CreatedTenant{
		Tenant: TenantRow{ID: t.ID, Name: t.Name, Subdomain: t.Subdomain, Status: t.Status,
			CommissionBPS: t.CommissionBPS, CreatedAt: s.now(), OwnerEmail: &email},
		OwnerEmail:   email,
		TempPassword: pw,
	}, nil
}

// UpdateTenant changes a tenant's status and/or commission. Nil means "leave it".
func (s *Store) UpdateTenant(ctx ctxT, id string, status *string, commissionBPS *int) error {
	if status != nil && *status != tenant.StatusActive && *status != tenant.StatusSuspended {
		return errors.New("status must be ACTIVE or SUSPENDED")
	}
	if commissionBPS != nil && (*commissionBPS < 0 || *commissionBPS > 10000) {
		return tenant.ErrBadCommission
	}
	var got string
	err := s.DB.QueryRow(ctx, `
		UPDATE tenants SET status = COALESCE($2, status), commission_bps = COALESCE($3, commission_bps)
		WHERE id = $1 RETURNING id::text`, id, status, commissionBPS).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenant.ErrTenantNotFound
	}
	return err
}

// ResetOwnerPassword sets a new random password on the tenant's first owner and returns it once.
func (s *Store) ResetOwnerPassword(ctx ctxT, tenantID string) (email, password string, err error) {
	password, err = auth.GeneratePassword()
	if err != nil {
		return "", "", err
	}
	var userID string
	err = s.DB.QueryRow(ctx, `
		SELECT id::text, email FROM users WHERE tenant_id = $1 AND role = 'owner' ORDER BY created_at LIMIT 1`, tenantID,
	).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", auth.ErrUserNotFound
	}
	if err != nil {
		return "", "", err
	}
	if err := auth.SetPassword(ctx, s.DB, userID, password); err != nil {
		return "", "", err
	}
	return email, password, nil
}

// AdminPayments lists payments across all tenants. tenantID "" means every tenant.
func (s *Store) AdminPayments(ctx ctxT, tenantID, status string, limit int) ([]PaymentRow, error) {
	rows, err := s.DB.Query(ctx, paymentSelect+`
		WHERE ($1 = '' OR p.tenant_id::text = $1) AND ($2 = '' OR p.status = $2)
		ORDER BY p.created_at DESC LIMIT $3`, tenantID, strings.ToUpper(status), clampLimit(limit, 100, 500))
	if err != nil {
		return nil, fmt.Errorf("payments: %w", err)
	}
	return scanPayments(rows)
}

// AdminDevices lists every router on the platform (no secrets).
func (s *Store) AdminDevices(ctx ctxT) ([]DeviceRow, error) {
	rows, err := s.DB.Query(ctx, deviceSelect+` ORDER BY t.name, d.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	return s.scanDevices(rows)
}
