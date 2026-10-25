// internal/plan/plan.go
package plan

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Plan represents a Wi-Fi bundle package.
type Plan struct {
	ID              string
	TenantID        string
	Name            string
	PriceCents      int
	DurationMinutes int
	DataLimitBytes  *int64
	SpeedLimit      *string
	CreatedAt       time.Time
}

// FormattedPrice formats cents to currency (e.g. 5000 -> "50.00").
func (p Plan) FormattedPrice() string {
	return fmt.Sprintf("%.2f", float64(p.PriceCents)/100.0)
}

// FormattedDuration formats minutes to human-readable strings (e.g. 60 -> "1 Hour", 1440 -> "24 Hours").
func (p Plan) FormattedDuration() string {
	if p.DurationMinutes < 60 {
		return fmt.Sprintf("%d Mins", p.DurationMinutes)
	}
	if p.DurationMinutes%1440 == 0 {
		days := p.DurationMinutes / 1440
		if days == 1 {
			return "1 Day"
		}
		return fmt.Sprintf("%d Days", days)
	}
	hours := p.DurationMinutes / 60
	if hours == 1 {
		return "1 Hour"
	}
	return fmt.Sprintf("%d Hours", hours)
}

// FormattedDataLimit formats data limits (e.g. 500MB, 5GB, or Unlimited).
func (p Plan) FormattedDataLimit() string {
	if p.DataLimitBytes == nil || *p.DataLimitBytes <= 0 {
		return "Unlimited Data"
	}
	bytes := *p.DataLimitBytes
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if bytes >= gb {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	}
	return fmt.Sprintf("%d MB", bytes/mb)
}

// ListByTenant retrieves all active plans for a specific tenant.
func ListByTenant(ctx context.Context, db *pgxpool.Pool, tenantID string) ([]Plan, error) {
	query := `
		SELECT id::text, tenant_id::text, name, price_cents, duration_minutes, data_limit_bytes, speed_limit, created_at
		FROM plans
		WHERE tenant_id = $1
		ORDER BY price_cents ASC
	`
	rows, err := db.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query plans: %w", err)
	}
	defer rows.Close()

	var plans []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(
			&p.ID,
			&p.TenantID,
			&p.Name,
			&p.PriceCents,
			&p.DurationMinutes,
			&p.DataLimitBytes,
			&p.SpeedLimit,
			&p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan plan row: %w", err)
		}
		plans = append(plans, p)
	}

	return plans, rows.Err()
}
