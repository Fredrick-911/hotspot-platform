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

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidMAC     = errors.New("invalid MAC address format")
	ErrDeviceNotFound = errors.New("device not found")
)

// Device represents a physical MikroTik router.
type Device struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	MacAddress   string    `json:"mac_address"`
	NasIPAddress *string   `json:"nas_ip_address,omitempty"`
	RadiusSecret string    `json:"radius_secret"`
	CreatedAt    time.Time `json:"created_at"`
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

// Register adds a new MikroTik router to a tenant and generates its RADIUS secret.
func Register(ctx context.Context, db *pgxpool.Pool, tenantID, rawMAC string, nasIP *string) (*Device, error) {
	normMAC, err := NormalizeMAC(rawMAC)
	if err != nil {
		return nil, err
	}

	secret, err := GenerateSecret()
	if err != nil {
		return nil, err
	}

	query := `
		INSERT INTO devices (tenant_id, mac_address, nas_ip_address, radius_secret)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, tenant_id::text, mac_address, nas_ip_address::text, radius_secret, created_at
	`

	var d Device
	err = db.QueryRow(ctx, query, tenantID, normMAC, nasIP, secret).Scan(
		&d.ID,
		&d.TenantID,
		&d.MacAddress,
		&d.NasIPAddress,
		&d.RadiusSecret,
		&d.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to register device: %w", err)
	}

	return &d, nil
}

// ListByTenant returns all registered routers for an ISP.
func ListByTenant(ctx context.Context, db *pgxpool.Pool, tenantID string) ([]Device, error) {
	query := `
		SELECT id::text, tenant_id::text, mac_address, nas_ip_address::text, radius_secret, created_at
		FROM devices
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`
	rows, err := db.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(
			&d.ID,
			&d.TenantID,
			&d.MacAddress,
			&d.NasIPAddress,
			&d.RadiusSecret,
			&d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// GenerateRouterOSScript produces the exact MikroTik setup script for this device.
func (d Device) GenerateRouterOSScript(serverHost string) string {
	return fmt.Sprintf(`# ========================================================
# MikroTik Quick Setup Script for Hotspot Platform
# Device MAC: %s
# ========================================================

# 1. Enable RADIUS for Hotspot Authentication
/radius add service=hotspot address=%s secret="%s" timeout=3000ms comment="Hotspot Platform Cloud"

# 2. Allow incoming Disconnect / CoA messages from Cloud
/radius incoming set accept=yes port=3799

# 3. Instruct Hotspot Profile to check FreeRADIUS
/ip hotspot profile set [find] use-radius=yes radius-accounting=yes radius-interim-update=1m

# 4. Whitelist Cloud Portal in Walled Garden (unauthenticated access)
/ip hotspot walled-garden add dst-host=%s action=allow comment="Cloud Portal Access"
`, d.MacAddress, serverHost, d.RadiusSecret, serverHost)
}
