package dashboard

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/jackc/pgx/v5"
)

// TenantOverview is the owner's home screen.
type TenantOverview struct {
	PeriodTotals
	Live
	DataUsed30dBytes int64      `json:"data_used_30d_bytes"`
	Daily            []DayPoint `json:"daily"`
	TopPlans         []PlanStat `json:"top_plans"`
	CommissionBPS    int        `json:"commission_bps"`
}

// TenantOverview builds the owner's home screen numbers.
func (s *Store) TenantOverview(ctx ctxT, tenantID string) (*TenantOverview, error) {
	var (
		o   TenantOverview
		err error
	)
	if o.PeriodTotals, err = s.periodTotals(ctx, tenantID); err != nil {
		return nil, err
	}
	if o.Live, err = s.live(ctx, tenantID); err != nil {
		return nil, err
	}
	if o.Daily, err = s.daily(ctx, tenantID); err != nil {
		return nil, err
	}

	_, _, month := s.periodStarts()
	rows, err := s.DB.Query(ctx, `
		SELECT pl.name, COUNT(*)::bigint, COALESCE(SUM(p.amount_cents), 0)::bigint
		FROM payments p JOIN plans pl ON pl.id = p.plan_id
		WHERE p.tenant_id = $1 AND p.status = 'SUCCESS' AND p.created_at >= $2
		GROUP BY pl.id, pl.name ORDER BY 3 DESC, 2 DESC LIMIT 5`, tenantID, month)
	if err != nil {
		return nil, fmt.Errorf("top plans: %w", err)
	}
	o.TopPlans = []PlanStat{}
	for rows.Next() {
		var p PlanStat
		if err := rows.Scan(&p.Name, &p.Sales, &p.GrossCents); err != nil {
			rows.Close()
			return nil, err
		}
		o.TopPlans = append(o.TopPlans, p)
	}
	rows.Close()

	if err := s.DB.QueryRow(ctx, `
		SELECT COALESCE(SUM(a.acctinputoctets + a.acctoutputoctets), 0)::bigint
		FROM radacct a JOIN sessions s ON s.username = a.username
		WHERE s.tenant_id = $1 AND a.acctstarttime >= $2`, tenantID, month).Scan(&o.DataUsed30dBytes); err != nil {
		return nil, fmt.Errorf("data used: %w", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT commission_bps FROM tenants WHERE id = $1`, tenantID).Scan(&o.CommissionBPS); err != nil {
		return nil, fmt.Errorf("commission: %w", err)
	}
	return &o, nil
}

const paymentSelect = `
SELECT p.id::text, p.external_reference, p.phone_number, pl.name, p.amount_cents::bigint, p.fee_cents::bigint,
       p.status, p.mpesa_receipt, p.result_desc, p.created_at, p.tenant_id::text, t.name, t.subdomain
FROM payments p
JOIN plans pl ON pl.id = p.plan_id
JOIN tenants t ON t.id = p.tenant_id`

func scanPayments(rows pgx.Rows) ([]PaymentRow, error) {
	defer rows.Close()
	out := []PaymentRow{}
	for rows.Next() {
		var p PaymentRow
		if err := rows.Scan(&p.ID, &p.Reference, &p.Phone, &p.PlanName, &p.AmountCents, &p.FeeCents,
			&p.Status, &p.Receipt, &p.Note, &p.CreatedAt, &p.TenantID, &p.TenantName, &p.Subdomain); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// Payments lists a tenant's payments, newest first. status is "" (all),
// "SUCCESS", "PENDING" or "FAILED".
func (s *Store) Payments(ctx ctxT, tenantID, status string, limit int) ([]PaymentRow, error) {
	rows, err := s.DB.Query(ctx, paymentSelect+`
		WHERE p.tenant_id = $1 AND ($2 = '' OR p.status = $2)
		ORDER BY p.created_at DESC LIMIT $3`, tenantID, strings.ToUpper(status), clampLimit(limit, 100, 500))
	if err != nil {
		return nil, fmt.Errorf("payments: %w", err)
	}
	return scanPayments(rows)
}

// PaymentByID loads one payment (used to push a live "new sale" event).
func (s *Store) PaymentByID(ctx ctxT, id string) (*PaymentRow, error) {
	rows, err := s.DB.Query(ctx, paymentSelect+` WHERE p.id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("payment: %w", err)
	}
	list, err := scanPayments(rows)
	if err != nil || len(list) == 0 {
		return nil, errors.Join(err, errors.New("payment not found"))
	}
	return &list[0], nil
}

// customerSelect works out a pass's real state from the session, the clock and radacct.
const customerSelect = `
SELECT s.id::text, s.phone_number, pl.name,
       CASE WHEN s.status = 'REVOKED' THEN 'REVOKED'
            WHEN s.expires_at <= NOW() THEN 'EXPIRED'
            WHEN s.data_limit_bytes > 0 AND u.used >= s.data_limit_bytes THEN 'DATA_FINISHED'
            WHEN u.online THEN 'ONLINE'
            ELSE 'ACTIVE' END,
       s.starts_at, s.expires_at, s.data_limit_bytes, u.used, u.last_seen, s.mac_address
FROM sessions s
JOIN plans pl ON pl.id = s.plan_id
LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(acctinputoctets + acctoutputoctets), 0)::bigint AS used,
           COALESCE(BOOL_OR(acctstoptime IS NULL AND COALESCE(acctupdatetime, acctstarttime) >= ` + onlineWindowSQL + `), false) AS online,
           MAX(COALESCE(acctupdatetime, acctstarttime)) AS last_seen
    FROM radacct WHERE username = s.username
) u ON true`

// Customers lists a tenant's passes, newest first. phonePrefix filters by phone.
func (s *Store) Customers(ctx ctxT, tenantID, phonePrefix string, limit int) ([]CustomerRow, error) {
	rows, err := s.DB.Query(ctx, customerSelect+`
		WHERE s.tenant_id = $1 AND ($2 = '' OR s.phone_number LIKE $2 || '%')
		ORDER BY s.starts_at DESC LIMIT $3`, tenantID, onlyDigits(phonePrefix), clampLimit(limit, 100, 500))
	if err != nil {
		return nil, fmt.Errorf("customers: %w", err)
	}
	defer rows.Close()
	out := []CustomerRow{}
	for rows.Next() {
		var c CustomerRow
		if err := rows.Scan(&c.SessionID, &c.Phone, &c.PlanName, &c.State, &c.StartsAt, &c.ExpiresAt,
			&c.DataLimitBytes, &c.DataUsedBytes, &c.LastSeenAt, &c.MacAddress); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Plans lists every plan of a tenant with what each has earned.
func (s *Store) Plans(ctx ctxT, tenantID string) ([]PlanRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT pl.id::text, pl.tenant_id::text, pl.name, pl.price_cents, pl.duration_minutes, pl.data_limit_bytes,
		       pl.speed_limit, pl.active, pl.created_at,
		       COUNT(p.id)::bigint, COALESCE(SUM(p.amount_cents), 0)::bigint
		FROM plans pl
		LEFT JOIN payments p ON p.plan_id = pl.id AND p.status = 'SUCCESS'
		WHERE pl.tenant_id = $1
		GROUP BY pl.id
		ORDER BY pl.active DESC, pl.price_cents ASC, pl.created_at ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("plans: %w", err)
	}
	defer rows.Close()
	out := []PlanRow{}
	for rows.Next() {
		var r PlanRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.PriceCents, &r.DurationMinutes, &r.DataLimitBytes,
			&r.SpeedLimit, &r.Active, &r.CreatedAt, &r.Sold, &r.GrossCents); err != nil {
			return nil, err
		}
		r.FormattedDur = r.FormattedDuration()
		r.FormattedDat = r.FormattedDataLimit()
		out = append(out, r)
	}
	return out, rows.Err()
}

const deviceSelect = `
SELECT d.id::text, d.name, d.mac_address, host(d.nas_ip_address), d.last_seen_at,
       COALESCE((SELECT COUNT(DISTINCT a.username) FROM radacct a
                  WHERE d.nas_ip_address IS NOT NULL AND a.nasipaddress = d.nas_ip_address AND a.acctstoptime IS NULL
                    AND COALESCE(a.acctupdatetime, a.acctstarttime) >= ` + onlineWindowSQL + `), 0)::bigint,
       d.created_at, t.name, t.subdomain
FROM devices d JOIN tenants t ON t.id = d.tenant_id`

func (s *Store) scanDevices(rows pgx.Rows) ([]DeviceRow, error) {
	defer rows.Close()
	out := []DeviceRow{}
	now := s.now()
	for rows.Next() {
		var d DeviceRow
		if err := rows.Scan(&d.ID, &d.Name, &d.MacAddress, &d.NasIP, &d.LastSeenAt, &d.ActiveUsers, &d.CreatedAt, &d.TenantName, &d.Subdomain); err != nil {
			return nil, err
		}
		d.Online = device.Device{LastSeenAt: d.LastSeenAt}.Online(now)
		out = append(out, d)
	}
	return out, rows.Err()
}

// Devices lists a tenant's routers WITHOUT their secrets.
func (s *Store) Devices(ctx ctxT, tenantID string) ([]DeviceRow, error) {
	rows, err := s.DB.Query(ctx, deviceSelect+` WHERE d.tenant_id = $1 ORDER BY d.created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	return s.scanDevices(rows)
}

// Announcements lists a tenant's recent announcements (newest first).
func (s *Store) Announcements(ctx ctxT, tenantID string, activeOnly bool) ([]Announcement, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id::text, message, created_at, expires_at FROM announcements
		WHERE tenant_id = $1 AND (NOT $2 OR expires_at > NOW())
		ORDER BY created_at DESC LIMIT 50`, tenantID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("announcements: %w", err)
	}
	defer rows.Close()
	out := []Announcement{}
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Message, &a.CreatedAt, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ErrAnnouncementNotFound means the announcement does not exist for this tenant.
var ErrAnnouncementNotFound = errors.New("announcement not found")

// ErrBadAnnouncement is returned for empty or over-long messages.
var ErrBadAnnouncement = errors.New("message must be 1-280 characters")

// CreateAnnouncement stores a message that is shown to customers for the given duration.
func (s *Store) CreateAnnouncement(ctx ctxT, tenantID, userID, message string, ttl time.Duration) (*Announcement, error) {
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > 280 {
		return nil, ErrBadAnnouncement
	}
	a := Announcement{Message: message}
	err := s.DB.QueryRow(ctx, `
		INSERT INTO announcements (tenant_id, message, created_by, expires_at)
		VALUES ($1, $2, $3, NOW() + make_interval(secs => $4))
		RETURNING id::text, created_at, expires_at`, tenantID, message, userID, ttl.Seconds(),
	).Scan(&a.ID, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("create announcement: %w", err)
	}
	return &a, nil
}

// DeleteAnnouncement removes one of the tenant's announcements.
func (s *Store) DeleteAnnouncement(ctx ctxT, tenantID, id string) error {
	var gone string
	err := s.DB.QueryRow(ctx, `DELETE FROM announcements WHERE id = $1 AND tenant_id = $2 RETURNING id::text`, id, tenantID).Scan(&gone)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAnnouncementNotFound
	}
	return err
}
