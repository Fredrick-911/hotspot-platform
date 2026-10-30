package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Roles.
const (
	RoleAdmin = "admin" // platform operator
	RoleOwner = "owner" // box owner (tenant)
)

var (
	ErrInvalidLogin = errors.New("invalid email or password")
	ErrEmailTaken   = errors.New("that email is already in use")
	ErrBadEmail     = errors.New("enter a valid email address")
	ErrUserNotFound = errors.New("user not found")
)

// User is a staff account.
type User struct {
	ID       string  `json:"id"`
	TenantID *string `json:"tenant_id,omitempty"`
	Email    string  `json:"email"`
	Name     string  `json:"name"`
	Role     string  `json:"role"`
	Active   bool    `json:"active"`
}

// NormalizeEmail trims and lower-cases an email and rejects obviously wrong ones.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	at := strings.LastIndex(e, "@")
	if len(e) > 254 || at < 1 || at == len(e)-1 || strings.ContainsAny(e, " \t\r\n,;<>\"'") || !strings.Contains(e[at:], ".") {
		return "", ErrBadEmail
	}
	return e, nil
}

// Create adds a staff user. tenantID must be "" for admins and set for owners.
func Create(ctx context.Context, db interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, tenantID, email, name, password, role string) (*User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	var tid *string
	if tenantID != "" {
		tid = &tenantID
	}

	u := User{Email: e, Name: strings.TrimSpace(name), Role: role, Active: true}
	err = db.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, name, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, tenant_id::text`,
		tid, e, u.Name, hash, role,
	).Scan(&u.ID, &u.TenantID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

// Authenticate checks an email + password and returns the user.
func Authenticate(ctx context.Context, db *pgxpool.Pool, email, password string) (*User, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	var (
		u    User
		hash string
	)
	err := db.QueryRow(ctx, `
		SELECT id::text, tenant_id::text, email, name, role, active, password_hash
		FROM users WHERE lower(email) = $1`, e,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.Active, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = CheckPassword(dummyHash, password) // same cost as a real check
		return nil, ErrInvalidLogin
	}
	if err != nil {
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	ok, err := CheckPassword(hash, password)
	if err != nil || !ok || !u.Active {
		return nil, ErrInvalidLogin
	}
	_, _ = db.Exec(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, u.ID)
	return &u, nil
}

// ByID loads a user, used on every dashboard request so a deactivated user is
// locked out immediately instead of when their cookie expires.
func ByID(ctx context.Context, db *pgxpool.Pool, id string) (*User, error) {
	var u User
	err := db.QueryRow(ctx, `
		SELECT id::text, tenant_id::text, email, name, role, active
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.Role, &u.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	return &u, nil
}

// SetPassword replaces a user's password.
func SetPassword(ctx context.Context, db interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}, userID, newPassword string) error {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	tag, err := db.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// AdminExists reports whether any platform admin has been created.
func AdminExists(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*)::int FROM users WHERE role = 'admin'`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
