package dashboard

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Account is the customer's own view of their pass.
type Account struct {
	SessionID       string         `json:"session_id"`
	Phone           string         `json:"phone"`
	Username        string         `json:"username"` // what the hotspot login form needs
	PIN             string         `json:"pin"`
	PlanName        string         `json:"plan_name"`
	State           string         `json:"state"` // ONLINE, ACTIVE, EXPIRED, REVOKED, DATA_FINISHED
	StartsAt        time.Time      `json:"starts_at"`
	ExpiresAt       time.Time      `json:"expires_at"`
	SecondsLeft     int64          `json:"seconds_left"`
	DurationMinutes int            `json:"duration_minutes"`
	DataLimitBytes  *int64         `json:"data_limit_bytes"`
	DataUsedBytes   int64          `json:"data_used_bytes"`
	DataLeftBytes   *int64         `json:"data_left_bytes"`
	SpeedLimit      *string        `json:"speed_limit"`
	Receipts        []PaymentRow   `json:"receipts"`
	Announcements   []Announcement `json:"announcements"`
}

// ErrAccountNotFound means the session no longer exists.
var ErrAccountNotFound = errors.New("account not found")

// Account loads one customer's pass, their receipts and the owner's current announcements.
func (s *Store) Account(ctx ctxT, tenantID, sessionID string) (*Account, error) {
	var a Account
	err := s.DB.QueryRow(ctx, `
		SELECT s.id::text, s.phone_number, s.username, s.password, pl.name,
		       CASE WHEN s.status = 'REVOKED' THEN 'REVOKED'
		            WHEN s.expires_at <= NOW() THEN 'EXPIRED'
		            WHEN s.data_limit_bytes > 0 AND u.used >= s.data_limit_bytes THEN 'DATA_FINISHED'
		            WHEN u.online THEN 'ONLINE'
		            ELSE 'ACTIVE' END,
		       s.starts_at, s.expires_at, GREATEST(EXTRACT(EPOCH FROM (s.expires_at - NOW())), 0)::bigint,
		       s.duration_minutes, s.data_limit_bytes, u.used, s.speed_limit
		FROM sessions s
		JOIN plans pl ON pl.id = s.plan_id
		LEFT JOIN LATERAL (
		    SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)::bigint AS used,
		           COALESCE(BOOL_OR(acctstoptime IS NULL AND COALESCE(acctupdatetime, acctstarttime) >= `+onlineWindowSQL+`), false) AS online
		    FROM radacct WHERE username = s.username
		) u ON true
		WHERE s.id = $1 AND s.tenant_id = $2`, sessionID, tenantID,
	).Scan(&a.SessionID, &a.Phone, &a.Username, &a.PIN, &a.PlanName, &a.State, &a.StartsAt, &a.ExpiresAt,
		&a.SecondsLeft, &a.DurationMinutes, &a.DataLimitBytes, &a.DataUsedBytes, &a.SpeedLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("account: %w", err)
	}
	if a.DataLimitBytes != nil && *a.DataLimitBytes > 0 {
		left := *a.DataLimitBytes - a.DataUsedBytes
		if left < 0 {
			left = 0
		}
		a.DataLeftBytes = &left
	}

	rows, err := s.DB.Query(ctx, paymentSelect+`
		WHERE p.tenant_id = $1 AND p.phone_number = $2
		ORDER BY p.created_at DESC LIMIT 15`, tenantID, a.Phone)
	if err != nil {
		return nil, fmt.Errorf("receipts: %w", err)
	}
	if a.Receipts, err = scanPayments(rows); err != nil {
		return nil, err
	}
	if a.Announcements, err = s.Announcements(ctx, tenantID, true); err != nil {
		return nil, err
	}
	return &a, nil
}
