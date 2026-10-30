// internal/device/device.go
package device

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidMAC     = errors.New("invalid MAC address format")
	ErrInvalidIP      = errors.New("router IP must be a valid private/tunnel IPv4 address, e.g. 10.8.0.2")
	ErrDeviceNotFound = errors.New("device not found")
	ErrDuplicateMAC   = errors.New("a router with that MAC address is already registered")
)

// HeartbeatOnlineWindow is how recently a router must have checked in to count as online.
const HeartbeatOnlineWindow = 3 * time.Minute

// Device represents a physical MikroTik router.
type Device struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	Name         string     `json:"name"`
	MacAddress   string     `json:"mac_address"`
	NasIPAddress *string    `json:"nas_ip_address,omitempty"`
	RadiusSecret string     `json:"radius_secret,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Online reports whether the router has sent a heartbeat recently.
func (d Device) Online(now time.Time) bool {
	return d.LastSeenAt != nil && now.Sub(*d.LastSeenAt) <= HeartbeatOnlineWindow
}

// GenerateSecret produces a secure 16-byte random hex string.
func GenerateSecret() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random secret: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// NormalizeMAC validates and standardizes a MAC address (e.g. 48:8f:5a:12:34:56).
func NormalizeMAC(raw string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil {
		return "", ErrInvalidMAC
	}
	return hw.String(), nil
}

// NormalizeIP checks the address the router uses on the WireGuard tunnel (the
// address FreeRADIUS sees requests coming from). Empty means "not set yet".
func NormalizeIP(raw string) (*string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return nil, ErrInvalidIP
	}
	out := ip.To4().String()
	return &out, nil
}

const deviceColumns = `id::text, tenant_id::text, name, mac_address, host(nas_ip_address), radius_secret, last_seen_at, created_at`

func scanDevice(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.TenantID, &d.Name, &d.MacAddress, &d.NasIPAddress, &d.RadiusSecret, &d.LastSeenAt, &d.CreatedAt)
	return d, err
}

// syncNAS keeps FreeRADIUS's "nas" table (its list of allowed routers and their
// secrets) equal to our devices. FreeRADIUS reads that table only when it
// STARTS (read_clients = yes in radius/mods-available/sql.tmpl); the timer
// script scripts/ops/radius-sync-clients.sh restarts it when the list changes.
func syncNAS(ctx context.Context, tx pgx.Tx, d Device) error {
	short := "hp-" + d.ID[:8]
	if _, err := tx.Exec(ctx, `DELETE FROM nas WHERE shortname = $1`, short); err != nil {
		return fmt.Errorf("clear nas row: %w", err)
	}
	if d.NasIPAddress == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO nas (nasname, shortname, type, secret, description)
		VALUES ($1, $2, 'other', $3, $4)`,
		*d.NasIPAddress, short, d.RadiusSecret, "hotspot-platform router "+d.MacAddress); err != nil {
		return fmt.Errorf("write nas row: %w", err)
	}
	return nil
}

// Register adds a new MikroTik router to a tenant and generates its RADIUS secret.
func Register(ctx context.Context, db *pgxpool.Pool, tenantID, name, rawMAC string, nasIP *string) (*Device, error) {
	normMAC, err := NormalizeMAC(rawMAC)
	if err != nil {
		return nil, err
	}
	if nasIP != nil {
		if nasIP, err = NormalizeIP(*nasIP); err != nil {
			return nil, err
		}
	}
	secret, err := GenerateSecret()
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		name = name[:100]
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	d, err := scanDevice(tx.QueryRow(ctx, `
		INSERT INTO devices (tenant_id, name, mac_address, nas_ip_address, radius_secret)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+deviceColumns, tenantID, name, normMAC, nasIP, secret))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateMAC
		}
		return nil, fmt.Errorf("failed to register device: %w", err)
	}
	if err := syncNAS(ctx, tx, d); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &d, nil
}

// Update renames a router and/or sets its tunnel IP, then updates FreeRADIUS's
// client list to match.
func Update(ctx context.Context, db *pgxpool.Pool, tenantID, id, name string, rawIP string) (*Device, error) {
	ip, err := NormalizeIP(rawIP)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		name = name[:100]
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	d, err := scanDevice(tx.QueryRow(ctx, `
		UPDATE devices SET name = $3, nas_ip_address = $4
		WHERE id = $1 AND tenant_id = $2
		RETURNING `+deviceColumns, id, tenantID, name, ip))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update device: %w", err)
	}
	if err := syncNAS(ctx, tx, d); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &d, nil
}

// Delete removes a router (and its FreeRADIUS client entry).
func Delete(ctx context.Context, db *pgxpool.Pool, tenantID, id string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var gone string
	err = tx.QueryRow(ctx, `DELETE FROM devices WHERE id = $1 AND tenant_id = $2 RETURNING id::text`, id, tenantID).Scan(&gone)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeviceNotFound
	}
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM nas WHERE shortname = $1`, "hp-"+gone[:8]); err != nil {
		return fmt.Errorf("clear nas row: %w", err)
	}
	return tx.Commit(ctx)
}

// ByID loads one router of a tenant (including its secret).
func ByID(ctx context.Context, db *pgxpool.Pool, tenantID, id string) (*Device, error) {
	d, err := scanDevice(db.QueryRow(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE id = $1 AND tenant_id = $2`, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}
	return &d, nil
}

// ListByTenant returns all registered routers for an ISP.
func ListByTenant(ctx context.Context, db *pgxpool.Pool, tenantID string) ([]Device, error) {
	rows, err := db.Query(ctx, `
		SELECT `+deviceColumns+` FROM devices WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query devices: %w", err)
	}
	defer rows.Close()

	devices := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// Heartbeat records that a router is alive. The router proves who it is with
// the RADIUS secret it was given; a wrong secret changes nothing.
func Heartbeat(ctx context.Context, db *pgxpool.Pool, id, secret string) error {
	var gone string
	err := db.QueryRow(ctx, `
		UPDATE devices SET last_seen_at = NOW()
		WHERE id::text = $1 AND radius_secret = $2
		RETURNING id::text`, id, secret).Scan(&gone)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeviceNotFound
	}
	return err
}

// ScriptOptions are the addresses a router needs.
type ScriptOptions struct {
	PortalHost   string // where the captive portal lives (walled garden + heartbeat)
	RadiusHost   string // where FreeRADIUS listens, as the router reaches it (WireGuard address)
	HeartbeatURL string // full URL the router calls once a minute; empty = no heartbeat
}

// GenerateRouterOSScript is kept for callers that only know one host.
func (d Device) GenerateRouterOSScript(serverHost string) string {
	return d.GenerateScript(ScriptOptions{PortalHost: serverHost, RadiusHost: serverHost})
}

// GenerateScript produces the exact MikroTik setup script for this device.
func (d Device) GenerateScript(o ScriptOptions) string {
	if o.RadiusHost == "" {
		o.RadiusHost = o.PortalHost
	}
	script := fmt.Sprintf(`# ========================================================
# MikroTik Quick Setup Script for Hotspot Platform
# Router: %s  (MAC %s)
# ========================================================

# 1. Enable RADIUS for Hotspot Authentication
/radius add service=hotspot address=%s secret="%s" timeout=3000ms comment="Hotspot Platform Cloud"

# 2. Allow incoming Disconnect / CoA messages from Cloud
/radius incoming set accept=yes port=3799

# 3. Instruct Hotspot Profile to check FreeRADIUS.
#    http-pap is required: the portal logs the customer in by posting the
#    username and PIN to the router's login page.
/ip hotspot profile set [find] use-radius=yes radius-accounting=yes radius-interim-update=1m login-by=cookie,http-pap

# 4. Whitelist Cloud Portal in Walled Garden (unauthenticated access)
/ip hotspot walled-garden add dst-host=%s action=allow comment="Cloud Portal Access"
`, displayName(d), d.MacAddress, o.RadiusHost, d.RadiusSecret, o.PortalHost)

	if o.HeartbeatURL != "" {
		script += fmt.Sprintf(`
# 5. Heartbeat: tells the dashboard this router is online (once a minute)
/system scheduler add name=hotspot-heartbeat interval=1m on-event="/tool fetch url=\"%s\" http-method=post http-data=\"key=%s\" keep-result=no" comment="Hotspot Platform heartbeat"
`, o.HeartbeatURL, d.RadiusSecret)
	}
	return script
}

func displayName(d Device) string {
	if d.Name != "" {
		return d.Name
	}
	return d.MacAddress
}
