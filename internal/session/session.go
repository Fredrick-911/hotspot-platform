// internal/session/session.go
package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session statuses stored in sessions.status.
const (
	StatusActive  = "ACTIVE"
	StatusExpired = "EXPIRED"
	StatusRevoked = "REVOKED"
)

var (
	ErrNoActiveSession = errors.New("no active session found")
	ErrDataExhausted   = errors.New("this pass has used all of its data")
)

// Session represents one purchased Wi-Fi pass.
type Session struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	PlanID          string    `json:"plan_id"`
	PaymentID       string    `json:"payment_id"`
	PhoneNumber     string    `json:"phone_number"`
	MacAddress      *string   `json:"mac_address,omitempty"`
	Username        string    `json:"username"`
	Password        string    `json:"-"` // the PIN: never put it in a dashboard list
	DurationMinutes int       `json:"duration_minutes"`
	StartsAt        time.Time `json:"starts_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Status          string    `json:"status"`
	DataLimitBytes  *int64    `json:"data_limit_bytes"`
	SpeedLimit      *string   `json:"speed_limit"`
}

const sessionColumns = `s.id::text, s.tenant_id::text, s.plan_id::text, s.payment_id::text, s.phone_number, s.mac_address,
	s.username, s.password, s.duration_minutes, s.starts_at, s.expires_at, s.status, s.data_limit_bytes, s.speed_limit`

func scanSession(row pgx.Row) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.TenantID, &s.PlanID, &s.PaymentID, &s.PhoneNumber, &s.MacAddress,
		&s.Username, &s.Password, &s.DurationMinutes, &s.StartsAt, &s.ExpiresAt, &s.Status, &s.DataLimitBytes, &s.SpeedLimit)
	return s, err
}

// GeneratePIN creates a random 4-digit numeric PIN (e.g. "8492").
func GeneratePIN() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return fmt.Sprintf("%04d", n.Int64()+1000)
}

const usernameAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// NewUsername returns a unique RADIUS username for one purchase:
// the phone number, a dash and 5 random characters ("0712345678-k3f9x").
// Usage is counted per username, so a new purchase must never reuse an old one.
func NewUsername(phone string) string {
	suffix := make([]byte, 5)
	for i := range suffix {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(usernameAlphabet))))
		suffix[i] = usernameAlphabet[n.Int64()]
	}
	return phone + "-" + string(suffix)
}

// Params is everything needed to turn one successful payment into a pass.
type Params struct {
	TenantID        string
	PlanID          string
	PaymentID       string
	Phone           string
	MAC             *string
	DurationMinutes int
	SpeedLimit      *string
	DataLimitBytes  *int64
}

// ByPayment returns the session created for a payment, if any.
func ByPayment(ctx context.Context, db *pgxpool.Pool, paymentID string) (*Session, error) {
	s, err := scanSession(db.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions s WHERE s.payment_id = $1`, paymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveSession
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	return &s, nil
}

// ByID returns a session of a tenant.
func ByID(ctx context.Context, db *pgxpool.Pool, tenantID, id string) (*Session, error) {
	s, err := scanSession(db.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions s WHERE s.id = $1 AND s.tenant_id = $2`, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveSession
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	return &s, nil
}

// Provision creates the session record AND writes the FreeRADIUS rows, in one
// transaction. It is safe to call twice for the same payment (a retried
// webhook plus the status poller): the second call returns the first session.
func Provision(ctx context.Context, db *pgxpool.Pool, p Params) (*Session, error) {
	if existing, err := ByPayment(ctx, db, p.PaymentID); err == nil {
		return existing, nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	username := NewUsername(p.Phone)
	pin := GeneratePIN()
	now := time.Now()
	expiresAt := now.Add(time.Duration(p.DurationMinutes) * time.Minute)

	// 1. Our own record. It keeps a copy of the limits that were SOLD, so
	//    editing the plan later never changes a pass someone already paid for.
	sess, err := scanSession(tx.QueryRow(ctx, `
		INSERT INTO sessions AS s (tenant_id, plan_id, payment_id, phone_number, mac_address, username, password,
		                           duration_minutes, starts_at, expires_at, status, data_limit_bytes, speed_limit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'ACTIVE', $11, $12)
		RETURNING `+sessionColumns,
		p.TenantID, p.PlanID, p.PaymentID, p.Phone, p.MAC, username, pin, p.DurationMinutes, now, expiresAt,
		p.DataLimitBytes, p.SpeedLimit))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Someone else provisioned this payment a moment ago.
			_ = tx.Rollback(ctx)
			return ByPayment(ctx, db, p.PaymentID)
		}
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// 2. FreeRADIUS: who may log in and until when / how much.
	checks := [][2]string{
		{"Cleartext-Password", pin},
		{"Expiration", strconv.FormatInt(expiresAt.Unix(), 10)}, // unix seconds: no time-zone surprises
	}
	if p.DataLimitBytes != nil && *p.DataLimitBytes > 0 {
		checks = append(checks, [2]string{"Max-Total-Octets", strconv.FormatInt(*p.DataLimitBytes, 10)})
	}
	for _, c := range checks {
		if _, err := tx.Exec(ctx,
			`INSERT INTO radcheck (username, attribute, op, value) VALUES ($1, $2, ':=', $3)`,
			username, c[0], c[1]); err != nil {
			return nil, fmt.Errorf("failed to write radcheck %s: %w", c[0], err)
		}
	}

	// 3. FreeRADIUS: things that are the same on every login.
	replies := [][2]string{{"Acct-Interim-Interval", "60"}}
	if p.SpeedLimit != nil && *p.SpeedLimit != "" {
		replies = append(replies, [2]string{"Mikrotik-Rate-Limit", *p.SpeedLimit})
	}
	for _, r := range replies {
		if _, err := tx.Exec(ctx,
			`INSERT INTO radreply (username, attribute, op, value) VALUES ($1, $2, ':=', $3)`,
			username, r[0], r[1]); err != nil {
			return nil, fmt.Errorf("failed to write radreply %s: %w", r[0], err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit session transaction: %w", err)
	}
	return &sess, nil
}

// Reconnect returns a customer's live pass when they give BOTH the phone number
// they paid with and the PIN shown after payment. (The old "phone OR pin" form
// let anyone guess one of 9,000 PINs and receive a stranger's login.)
func Reconnect(ctx context.Context, db *pgxpool.Pool, tenantID, phone, pin string, newMAC *string) (*Session, error) {
	var used int64
	row := db.QueryRow(ctx, `
		SELECT `+sessionColumns+`,
		       COALESCE((SELECT SUM(u.acctinputoctets + u.acctoutputoctets) FROM radacct u WHERE u.username = s.username), 0)::bigint
		FROM sessions s
		WHERE s.tenant_id = $1 AND s.phone_number = $2 AND s.password = $3
		  AND s.status = 'ACTIVE' AND s.expires_at > NOW()
		ORDER BY s.expires_at DESC
		LIMIT 1`, tenantID, phone, pin)
	var sess Session
	err := row.Scan(&sess.ID, &sess.TenantID, &sess.PlanID, &sess.PaymentID, &sess.PhoneNumber, &sess.MacAddress,
		&sess.Username, &sess.Password, &sess.DurationMinutes, &sess.StartsAt, &sess.ExpiresAt, &sess.Status,
		&sess.DataLimitBytes, &sess.SpeedLimit, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveSession
	}
	if err != nil {
		return nil, fmt.Errorf("reconnect lookup: %w", err)
	}
	if sess.DataLimitBytes != nil && *sess.DataLimitBytes > 0 && used >= *sess.DataLimitBytes {
		return nil, ErrDataExhausted
	}

	if newMAC != nil && *newMAC != "" {
		_, _ = db.Exec(ctx, "UPDATE sessions SET mac_address = $1 WHERE id = $2", *newMAC, sess.ID)
	}
	return &sess, nil
}

// Revoke ends a pass now: the RADIUS rows are deleted (so the next login is
// rejected) and the session is marked REVOKED. It returns the session so the
// caller can also disconnect the user if they are online.
func Revoke(ctx context.Context, db *pgxpool.Pool, tenantID, sessionID string) (*Session, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sess, err := scanSession(tx.QueryRow(ctx, `
		UPDATE sessions s SET status = 'REVOKED'
		WHERE s.id = $1 AND s.tenant_id = $2 AND s.status = 'ACTIVE'
		RETURNING `+sessionColumns, sessionID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveSession
	}
	if err != nil {
		return nil, fmt.Errorf("revoke: %w", err)
	}
	for _, q := range []string{`DELETE FROM radcheck WHERE username = $1`, `DELETE FROM radreply WHERE username = $1`} {
		if _, err := tx.Exec(ctx, q, sess.Username); err != nil {
			return nil, fmt.Errorf("clear radius rows: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &sess, nil
}
