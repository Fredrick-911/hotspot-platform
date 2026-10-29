// internal/dashboard/dashboard.go
package dashboard

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats holds real-time revenue and network health metrics.
type Stats struct {
	TodayRevenueCents int
	ActiveSessions    int
	TotalDevices      int
}

// FormattedRevenue returns earnings in KES (e.g. 1450.00).
func (s Stats) FormattedRevenue() string {
	return fmt.Sprintf("%.2f", float64(s.TodayRevenueCents)/100.0)
}

// CustomerSession represents a combined view of payment, session, and plan for customer support.
type CustomerSession struct {
	SessionID       string
	PhoneNumber     string
	MpesaReceipt    string
	PlanName        string
	AmountCents     int
	Pin             string
	DurationMinutes int
	ExpiresAt       time.Time
	Status          string
	CreatedAt       time.Time
}

// FormattedAmount returns payment amount formatted in KES.
func (cs CustomerSession) FormattedAmount() string {
	return fmt.Sprintf("%.2f", float64(cs.AmountCents)/100.0)
}

// TimeRemaining returns a human-readable duration countdown until pass expires.
func (cs CustomerSession) TimeRemaining() string {
	if cs.Status != "ACTIVE" {
		return "Expired"
	}

	remaining := time.Until(cs.ExpiresAt).Round(time.Minute)
	if remaining <= 0 {
		return "Expired"
	}

	hours := int(remaining.Hours())
	minutes := int(remaining.Minutes()) % 60
	if hours >= 24 {
		days := hours / 24
		return fmt.Sprintf("%d Days left", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm left", hours, minutes)
	}
	return fmt.Sprintf("%dm left", minutes)
}

// TenantSettings represents self-service branding and payment settings.
type TenantSettings struct {
	Name             string
	SupportPhone     string
	PayHeroChannelID int
}

// GetTenantSettings loads the current settings for a tenant.
func GetTenantSettings(ctx context.Context, db *pgxpool.Pool, tenantID string) (TenantSettings, error) {
	var ts TenantSettings
	query := `SELECT name, COALESCE(support_phone, ''), COALESCE(payhero_channel_id, 0) FROM tenants WHERE id = $1`
	err := db.QueryRow(ctx, query, tenantID).Scan(&ts.Name, &ts.SupportPhone, &ts.PayHeroChannelID)
	return ts, err
}

// UpdateTenantSettings updates branding and channel settings.
func UpdateTenantSettings(ctx context.Context, db *pgxpool.Pool, tenantID, name, phone string, channelID int) error {
	query := `UPDATE tenants SET name = $1, support_phone = $2, payhero_channel_id = $3 WHERE id = $4`
	_, err := db.Exec(ctx, query, strings.TrimSpace(name), strings.TrimSpace(phone), channelID, tenantID)
	return err
}

// RevokeSession manually kicks a customer pass.
func RevokeSession(ctx context.Context, db *pgxpool.Pool, tenantID, sessionID string) error {
	// Find username to clean up from FreeRADIUS
	var username string
	err := db.QueryRow(ctx, `SELECT username FROM sessions WHERE id = $1 AND tenant_id = $2`, sessionID, tenantID).Scan(&username)
	if err != nil {
		return err
	}

	// 1. Mark session EXPIRED in database
	_, _ = db.Exec(ctx, `UPDATE sessions SET status = 'EXPIRED', expires_at = NOW() WHERE id = $1`, sessionID)

	// 2. Remove credentials from FreeRADIUS radcheck so router rejects reconnects
	_, _ = db.Exec(ctx, `DELETE FROM radcheck WHERE username = $1`, username)

	return nil
}

// GetStats calculates live metrics for a tenant.
func GetStats(ctx context.Context, db *pgxpool.Pool, tenantID string) (Stats, error) {
	var s Stats

	queryRevenue := `
		SELECT COALESCE(SUM(amount_cents), 0)
		FROM payments
		WHERE tenant_id = $1 AND status = 'SUCCESS' AND created_at >= CURRENT_DATE
	`
	if err := db.QueryRow(ctx, queryRevenue, tenantID).Scan(&s.TodayRevenueCents); err != nil {
		return s, fmt.Errorf("failed to calculate revenue: %w", err)
	}

	querySessions := `
		SELECT COUNT(*)
		FROM sessions
		WHERE tenant_id = $1 AND status = 'ACTIVE' AND expires_at > NOW()
	`
	if err := db.QueryRow(ctx, querySessions, tenantID).Scan(&s.ActiveSessions); err != nil {
		return s, fmt.Errorf("failed to count active sessions: %w", err)
	}

	queryDevices := `
		SELECT COUNT(*)
		FROM devices
		WHERE tenant_id = $1
	`
	if err := db.QueryRow(ctx, queryDevices, tenantID).Scan(&s.TotalDevices); err != nil {
		return s, fmt.Errorf("failed to count devices: %w", err)
	}

	return s, nil
}

// ListCustomerSessions retrieves recent sessions with optional search filtering.
func ListCustomerSessions(ctx context.Context, db *pgxpool.Pool, tenantID, searchQuery string) ([]CustomerSession, error) {
	baseQuery := `
		SELECT s.id::text, s.phone_number, COALESCE(p.mpesa_receipt, '-'), pl.name,
		       p.amount_cents, s.password, s.duration_minutes, s.expires_at, s.status, s.created_at
		FROM sessions s
		JOIN payments p ON p.id = s.payment_id
		JOIN plans pl ON pl.id = s.plan_id
		WHERE s.tenant_id = $1
	`

	var args []any
	args = append(args, tenantID)

	if search := strings.TrimSpace(searchQuery); search != "" {
		baseQuery += ` AND (s.phone_number ILIKE $2 OR p.mpesa_receipt ILIKE $2 OR s.password ILIKE $2)`
		args = append(args, "%"+search+"%")
	}

	baseQuery += ` ORDER BY s.created_at DESC LIMIT 50`

	rows, err := db.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query customer sessions: %w", err)
	}
	defer rows.Close()

	var sessions []CustomerSession
	for rows.Next() {
		var cs CustomerSession
		if err := rows.Scan(
			&cs.SessionID,
			&cs.PhoneNumber,
			&cs.MpesaReceipt,
			&cs.PlanName,
			&cs.AmountCents,
			&cs.Pin,
			&cs.DurationMinutes,
			&cs.ExpiresAt,
			&cs.Status,
			&cs.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan session row: %w", err)
		}
		sessions = append(sessions, cs)
	}

	return sessions, rows.Err()
}

// GenerateCSVExport produces a downloadable CSV file of all transactions for the tenant.
func GenerateCSVExport(ctx context.Context, db *pgxpool.Pool, tenantID string) ([]byte, error) {
	sessions, err := ListCustomerSessions(ctx, db, tenantID, "")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// Write CSV Header
	_ = writer.Write([]string{"Date", "Phone Number", "M-Pesa Receipt", "Package Name", "Amount (KES)", "Wi-Fi PIN", "Status"})

	for _, s := range sessions {
		_ = writer.Write([]string{
			s.CreatedAt.Format("2006-01-02 15:04:05"),
			s.PhoneNumber,
			s.MpesaReceipt,
			s.PlanName,
			s.FormattedAmount(),
			s.Pin,
			s.Status,
		})
	}

	writer.Flush()
	return buf.Bytes(), nil
}

// CreatePlanInput defines user input for new packages.
type CreatePlanInput struct {
	Name            string
	PriceKES        float64
	DurationMinutes int
	SpeedLimit      string
	DataLimitMB     int64
}

// CreatePlan inserts a new internet package for the tenant.
func CreatePlan(ctx context.Context, db *pgxpool.Pool, tenantID string, input CreatePlanInput) error {
	priceCents := int(input.PriceKES * 100)

	var dataBytes *int64
	if input.DataLimitMB > 0 {
		bytes := input.DataLimitMB * 1024 * 1024
		dataBytes = &bytes
	}

	var speed *string
	if input.SpeedLimit != "" {
		s := input.SpeedLimit
		speed = &s
	}

	query := `
		INSERT INTO plans (tenant_id, name, price_cents, duration_minutes, data_limit_bytes, speed_limit)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := db.Exec(ctx, query, tenantID, input.Name, priceCents, input.DurationMinutes, dataBytes, speed)
	return err
}

// DeletePlan removes an internet package.
func DeletePlan(ctx context.Context, db *pgxpool.Pool, tenantID, planID string) error {
	query := `DELETE FROM plans WHERE id = $1 AND tenant_id = $2`
	tag, err := db.Exec(ctx, query, planID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("plan not found or unauthorized")
	}
	return nil
}

// DashboardViewData packages everything needed to render the owner dashboard.
type DashboardViewData struct {
	TenantName     string
	Subdomain      string
	Settings       TenantSettings
	Stats          Stats
	Sessions       []CustomerSession
	Plans          []plan.Plan
	Devices        []device.Device
	SearchQuery    string
	ServerHost     string
	SuccessMessage string
	ErrorMessage   string
}
