// internal/session/session.go
package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Session represents an active Wi-Fi session.
type Session struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	PlanID          string    `json:"plan_id"`
	PaymentID       string    `json:"payment_id"`
	PhoneNumber     string    `json:"phone_number"`
	MacAddress      *string   `json:"mac_address,omitempty"`
	Username        string    `json:"username"`
	Password        string    `json:"password"`
	DurationMinutes int       `json:"duration_minutes"`
	StartsAt        time.Time `json:"starts_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Status          string    `json:"status"`
}

// GeneratePIN creates a random 4-digit numeric PIN (e.g. "8492").
func GeneratePIN() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return fmt.Sprintf("%04d", n.Int64()+1000)
}

// ProvisionUser creates the session record AND writes directly to FreeRADIUS tables.
func ProvisionUser(ctx context.Context, db *pgxpool.Pool, tenantID, planID, paymentID, phone string, mac *string, durationMinutes int, speedLimit *string, dataLimitBytes *int64) (*Session, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	username := phone
	pin := GeneratePIN()
	now := time.Now()
	expiresAt := now.Add(time.Duration(durationMinutes) * time.Minute)
	durationSeconds := durationMinutes * 60

	// 1. Insert into our application sessions table
	var sess Session
	querySession := `
		INSERT INTO sessions (tenant_id, plan_id, payment_id, phone_number, mac_address, username, password, duration_minutes, starts_at, expires_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'ACTIVE')
		RETURNING id::text, tenant_id::text, plan_id::text, payment_id::text, phone_number, mac_address, username, password, duration_minutes, starts_at, expires_at, status
	`
	err = tx.QueryRow(ctx, querySession, tenantID, planID, paymentID, phone, mac, username, pin, durationMinutes, now, expiresAt).Scan(
		&sess.ID, &sess.TenantID, &sess.PlanID, &sess.PaymentID, &sess.PhoneNumber, &sess.MacAddress,
		&sess.Username, &sess.Password, &sess.DurationMinutes, &sess.StartsAt, &sess.ExpiresAt, &sess.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// 2. FreeRADIUS Provisioning: radcheck (Credentials)
	// Delete any old credentials for this phone first
	_, _ = tx.Exec(ctx, "DELETE FROM radcheck WHERE username = $1", username)
	_, _ = tx.Exec(ctx, "DELETE FROM radreply WHERE username = $1", username)

	_, err = tx.Exec(ctx, `
		INSERT INTO radcheck (username, attribute, op, value)
		VALUES ($1, 'Cleartext-Password', ':=', $2)
	`, username, pin)
	if err != nil {
		return nil, fmt.Errorf("failed to write radcheck: %w", err)
	}

	// 3. FreeRADIUS Provisioning: radreply (Limits: Time, Speed, Data)
	// Session timeout in seconds
	_, err = tx.Exec(ctx, `
		INSERT INTO radreply (username, attribute, op, value)
		VALUES ($1, 'Session-Timeout', ':=', $2)
	`, username, fmt.Sprintf("%d", durationSeconds))
	if err != nil {
		return nil, fmt.Errorf("failed to write Session-Timeout to radreply: %w", err)
	}

	// MikroTik Rate Limit (Speed cap)
	if speedLimit != nil && *speedLimit != "" {
		_, err = tx.Exec(ctx, `
			INSERT INTO radreply (username, attribute, op, value)
			VALUES ($1, 'Mikrotik-Rate-Limit', ':=', $2)
		`, username, *speedLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to write Mikrotik-Rate-Limit to radreply: %w", err)
		}
	}

	// MikroTik Data Cap (if set)
	if dataLimitBytes != nil && *dataLimitBytes > 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO radreply (username, attribute, op, value)
			VALUES ($1, 'Mikrotik-Total-Limit', ':=', $2)
		`, username, fmt.Sprintf("%d", *dataLimitBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to write Mikrotik-Total-Limit to radreply: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit session transaction: %w", err)
	}

	return &sess, nil
}

// ReconnectByPhoneOrPIN verifies if a phone or PIN has an active, non-expired session.
func ReconnectByPhoneOrPIN(ctx context.Context, db *pgxpool.Pool, tenantID, phoneOrPIN string, newMAC *string) (*Session, error) {
	query := `
		SELECT id::text, tenant_id::text, plan_id::text, payment_id::text, phone_number, mac_address,
		       username, password, duration_minutes, starts_at, expires_at, status
		FROM sessions
		WHERE tenant_id = $1 
		  AND (phone_number = $2 OR password = $2)
		  AND status = 'ACTIVE' 
		  AND expires_at > NOW()
		ORDER BY expires_at DESC
		LIMIT 1
	`
	var sess Session
	err := db.QueryRow(ctx, query, tenantID, phoneOrPIN).Scan(
		&sess.ID, &sess.TenantID, &sess.PlanID, &sess.PaymentID, &sess.PhoneNumber, &sess.MacAddress,
		&sess.Username, &sess.Password, &sess.DurationMinutes, &sess.StartsAt, &sess.ExpiresAt, &sess.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("no active session found: %w", err)
	}

	// Update with new MAC address if provided
	if newMAC != nil && *newMAC != "" {
		_, _ = db.Exec(ctx, "UPDATE sessions SET mac_address = $1 WHERE id = $2", *newMAC, sess.ID)
	}

	return &sess, nil
}
