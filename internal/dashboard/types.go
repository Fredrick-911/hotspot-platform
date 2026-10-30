package dashboard

import (
	"context"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/plan"
)

type ctxT = context.Context

// PlanStat is a best-seller line.
type PlanStat struct {
	Name         string `json:"name"`
	Sales        int64  `json:"sales"`
	GrossCents   int64  `json:"gross_cents"`
	TenantName   string `json:"tenant_name,omitempty"`
	TenantDomain string `json:"tenant_subdomain,omitempty"`
}

// PaymentRow is one line of a payments table.
type PaymentRow struct {
	ID          string    `json:"id"`
	Reference   string    `json:"reference"`
	Phone       string    `json:"phone"`
	PlanName    string    `json:"plan_name"`
	AmountCents int64     `json:"amount_cents"`
	FeeCents    int64     `json:"fee_cents"`
	Status      string    `json:"status"`
	Receipt     *string   `json:"receipt,omitempty"`
	Note        *string   `json:"note,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	TenantID    string    `json:"tenant_id,omitempty"`
	TenantName  string    `json:"tenant_name,omitempty"`
	Subdomain   string    `json:"tenant_subdomain,omitempty"`
}

// CustomerRow is one purchased pass in the owner's "Customers" table.
type CustomerRow struct {
	SessionID      string     `json:"session_id"`
	Phone          string     `json:"phone"`
	PlanName       string     `json:"plan_name"`
	State          string     `json:"state"` // ONLINE, ACTIVE, EXPIRED, REVOKED, DATA_FINISHED
	StartsAt       time.Time  `json:"starts_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	DataLimitBytes *int64     `json:"data_limit_bytes"`
	DataUsedBytes  int64      `json:"data_used_bytes"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	MacAddress     *string    `json:"mac_address,omitempty"`
}

// DeviceRow is one router in a routers table.
type DeviceRow struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	MacAddress  string     `json:"mac_address"`
	NasIP       *string    `json:"nas_ip_address"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	Online      bool       `json:"online"`
	ActiveUsers int64      `json:"active_users"`
	CreatedAt   time.Time  `json:"created_at"`
	TenantName  string     `json:"tenant_name,omitempty"`
	Subdomain   string     `json:"tenant_subdomain,omitempty"`
}

// PlanRow is a plan plus what it has earned.
type PlanRow struct {
	plan.Plan
	Sold         int64  `json:"sold"`
	GrossCents   int64  `json:"gross_cents"`
	FormattedDur string `json:"duration_text"`
	FormattedDat string `json:"data_text"`
}

// Announcement is a message an owner pushed to everyone on their WiFi.
type Announcement struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
