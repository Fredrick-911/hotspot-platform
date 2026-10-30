// internal/plan/plan.go
package plan

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Plan represents a Wi-Fi bundle package.
type Plan struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	Name            string    `json:"name"`
	PriceCents      int       `json:"price_cents"`
	DurationMinutes int       `json:"duration_minutes"`
	DataLimitBytes  *int64    `json:"data_limit_bytes"`
	SpeedLimit      *string   `json:"speed_limit"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
}

// Limits for what an owner may create.
const (
	MaxDurationMin = 365 * 24 * 60
	MinDataBytes   = 1 << 20 // 1 MB
	MaxDataBytes   = 1 << 40 // 1 TB
	maxNameLen     = 60
	maxPriceKES    = 20000 // far below M-Pesa's own per-transaction ceiling; a sane plan price
)

var (
	ErrNotFound = errors.New("plan not found")

	// MikroTik rate-limit format: "upload/download", e.g. "2M/5M" or "512k/1M".
	speedRE = regexp.MustCompile(`^[1-9][0-9]{0,3}[kKmM]/[1-9][0-9]{0,3}[kKmM]$`)
)

// Input is what an owner submits when creating or editing a plan.
type Input struct {
	Name            string  `json:"name"`
	PriceKES        int     `json:"price_kes"`
	DurationMinutes int     `json:"duration_minutes"`
	DataLimitMB     *int64  `json:"data_limit_mb"` // nil or 0 = unlimited
	SpeedLimit      *string `json:"speed_limit"`   // nil or "" = no cap
}

// Validate checks the input and returns a clear message for the owner.
func (in *Input) Validate() error {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "" || len(in.Name) > maxNameLen:
		return fmt.Errorf("name must be 1-%d characters", maxNameLen)
	case in.PriceKES < 1 || in.PriceKES > maxPriceKES:
		return fmt.Errorf("price must be between KES 1 and KES %d", maxPriceKES)
	case in.DurationMinutes < 1 || in.DurationMinutes > MaxDurationMin:
		return errors.New("duration must be between 1 minute and 365 days")
	}
	if in.DataLimitMB != nil && *in.DataLimitMB != 0 {
		b := *in.DataLimitMB << 20
		if *in.DataLimitMB < 0 || b < MinDataBytes || b > MaxDataBytes {
			return errors.New("data limit must be between 1 MB and 1 TB (or empty for unlimited)")
		}
	}
	if in.SpeedLimit != nil {
		s := strings.TrimSpace(*in.SpeedLimit)
		in.SpeedLimit = &s
		if s != "" && !speedRE.MatchString(s) {
			return errors.New(`speed limit must look like "2M/5M" (upload/download) or be empty`)
		}
	}
	return nil
}

func (in Input) dataBytes() *int64 {
	if in.DataLimitMB == nil || *in.DataLimitMB == 0 {
		return nil
	}
	b := *in.DataLimitMB << 20
	return &b
}

func (in Input) speed() *string {
	if in.SpeedLimit == nil || *in.SpeedLimit == "" {
		return nil
	}
	return in.SpeedLimit
}

// FormattedPrice formats cents to currency (e.g. 5000 -> "50.00").
func (p Plan) FormattedPrice() string {
	return fmt.Sprintf("%.2f", float64(p.PriceCents)/100.0)
}

// FormattedDuration formats minutes to human-readable strings (e.g. 60 -> "1 Hour", 1440 -> "24 Hours").
func (p Plan) FormattedDuration() string { return FormatMinutes(p.DurationMinutes) }

// FormatMinutes turns minutes into "30 Mins", "1 Hour", "3 Hours", "1 Day", "7 Days".
func FormatMinutes(m int) string {
	if m < 60 {
		return fmt.Sprintf("%d Mins", m)
	}
	if m%1440 == 0 {
		days := m / 1440
		if days == 1 {
			return "1 Day"
		}
		return fmt.Sprintf("%d Days", days)
	}
	hours := m / 60
	if hours == 1 {
		return "1 Hour"
	}
	return fmt.Sprintf("%d Hours", hours)
}

// FormattedDataLimit formats data limits (e.g. 500MB, 5GB, or Unlimited).
func (p Plan) FormattedDataLimit() string { return FormatBytes(p.DataLimitBytes) }

// FormatBytes turns a limit into "500 MB", "1.5 GB" or "Unlimited Data".
func FormatBytes(limit *int64) string {
	if limit == nil || *limit <= 0 {
		return "Unlimited Data"
	}
	bytes := *limit
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if bytes >= gb {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	}
	return fmt.Sprintf("%d MB", bytes/mb)
}

const planColumns = `id::text, tenant_id::text, name, price_cents, duration_minutes, data_limit_bytes, speed_limit, active, created_at`

func scanPlan(row pgx.Row) (Plan, error) {
	var p Plan
	err := row.Scan(&p.ID, &p.TenantID, &p.Name, &p.PriceCents, &p.DurationMinutes, &p.DataLimitBytes, &p.SpeedLimit, &p.Active, &p.CreatedAt)
	return p, err
}

// ListByTenant returns the plans customers can BUY (active only), cheapest first.
func ListByTenant(ctx context.Context, db *pgxpool.Pool, tenantID string) ([]Plan, error) {
	return list(ctx, db, tenantID, true)
}

// ListAll returns every plan including archived ones, for the owner dashboard.
func ListAll(ctx context.Context, db *pgxpool.Pool, tenantID string) ([]Plan, error) {
	return list(ctx, db, tenantID, false)
}

func list(ctx context.Context, db *pgxpool.Pool, tenantID string, activeOnly bool) ([]Plan, error) {
	q := `SELECT ` + planColumns + ` FROM plans WHERE tenant_id = $1`
	if activeOnly {
		q += ` AND active`
	}
	q += ` ORDER BY active DESC, price_cents ASC, created_at ASC`
	rows, err := db.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query plans: %w", err)
	}
	defer rows.Close()

	plans := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan plan row: %w", err)
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

// Create adds a plan for a tenant.
func Create(ctx context.Context, db *pgxpool.Pool, tenantID string, in Input) (*Plan, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	p, err := scanPlan(db.QueryRow(ctx, `
		INSERT INTO plans (tenant_id, name, price_cents, duration_minutes, data_limit_bytes, speed_limit)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+planColumns,
		tenantID, in.Name, in.PriceKES*100, in.DurationMinutes, in.dataBytes(), in.speed()))
	if err != nil {
		return nil, fmt.Errorf("failed to create plan: %w", err)
	}
	return &p, nil
}

// Update edits a plan. Passes already sold keep what they were sold with
// (sessions store their own copy of the limits).
func Update(ctx context.Context, db *pgxpool.Pool, tenantID, planID string, in Input) (*Plan, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	p, err := scanPlan(db.QueryRow(ctx, `
		UPDATE plans
		SET name = $3, price_cents = $4, duration_minutes = $5, data_limit_bytes = $6, speed_limit = $7, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
		RETURNING `+planColumns,
		planID, tenantID, in.Name, in.PriceKES*100, in.DurationMinutes, in.dataBytes(), in.speed()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update plan: %w", err)
	}
	return &p, nil
}

// SetActive archives (false) or restores (true) a plan. Archived plans are
// hidden from the portal but stay in the history of everything sold with them.
func SetActive(ctx context.Context, db *pgxpool.Pool, tenantID, planID string, active bool) error {
	var id string
	err := db.QueryRow(ctx,
		`UPDATE plans SET active = $3, updated_at = NOW() WHERE id = $1 AND tenant_id = $2 RETURNING id::text`,
		planID, tenantID, active).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
