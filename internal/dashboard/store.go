// Package dashboard holds the read models (and a few admin writes) behind the
// three dashboards: platform admin, box owner (tenant) and customer.
//
// Every tenant-facing query takes a tenant ID and filters on it, so one owner
// can never see another owner's numbers.
package dashboard

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store runs the dashboard queries.
type Store struct {
	DB *pgxpool.Pool
	// TZ is the business time zone ("Africa/Nairobi"): "today" and the daily
	// chart follow it, not the server's clock.
	TZ  string
	loc *time.Location
	now func() time.Time
}

// NewStore returns a Store. It fails if the time zone name is unknown.
func NewStore(db *pgxpool.Pool, tz string) (*Store, error) {
	if tz == "" {
		tz = "Africa/Nairobi"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q: %w", tz, err)
	}
	return &Store{DB: db, TZ: tz, loc: loc, now: time.Now}, nil
}

// OnlineWindow: a user counts as "online" if their router reported usage this recently.
const onlineWindowSQL = `NOW() - interval '5 minutes'`

// periodStarts returns the start of today (local midnight), 7 days ago and 30 days ago.
func (s *Store) periodStarts() (today, week, month time.Time) {
	n := s.now().In(s.loc)
	today = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.loc)
	return today, n.AddDate(0, 0, -7), n.AddDate(0, 0, -30)
}

// Totals is money and sales for one period. All money is in cents (KES x 100).
type Totals struct {
	GrossCents int64 `json:"gross_cents"` // what customers paid
	FeeCents   int64 `json:"fee_cents"`   // the platform's commission
	NetCents   int64 `json:"net_cents"`   // what the owner keeps (gross - fee)
	Sales      int64 `json:"sales"`
}

func totals(gross, fee, sales int64) Totals {
	return Totals{GrossCents: gross, FeeCents: fee, NetCents: gross - fee, Sales: sales}
}

// DayPoint is one bar of the 14-day chart.
type DayPoint struct {
	Date       string `json:"date"` // YYYY-MM-DD in the business time zone
	GrossCents int64  `json:"gross_cents"`
	FeeCents   int64  `json:"fee_cents"`
	Sales      int64  `json:"sales"`
}

// PeriodTotals groups the headline numbers.
type PeriodTotals struct {
	Today Totals `json:"today"`
	Week  Totals `json:"week"`
	Month Totals `json:"month"`
	All   Totals `json:"all"`
}

// totalsQuery sums successful payments for four periods. $1 is the tenant id
// (” means all tenants); $2..$4 are the period starts.
const totalsQuery = `
SELECT
  COALESCE(SUM(amount_cents) FILTER (WHERE created_at >= $2), 0)::bigint,
  COALESCE(SUM(fee_cents)    FILTER (WHERE created_at >= $2), 0)::bigint,
  COUNT(*) FILTER (WHERE created_at >= $2),
  COALESCE(SUM(amount_cents) FILTER (WHERE created_at >= $3), 0)::bigint,
  COALESCE(SUM(fee_cents)    FILTER (WHERE created_at >= $3), 0)::bigint,
  COUNT(*) FILTER (WHERE created_at >= $3),
  COALESCE(SUM(amount_cents) FILTER (WHERE created_at >= $4), 0)::bigint,
  COALESCE(SUM(fee_cents)    FILTER (WHERE created_at >= $4), 0)::bigint,
  COUNT(*) FILTER (WHERE created_at >= $4),
  COALESCE(SUM(amount_cents), 0)::bigint,
  COALESCE(SUM(fee_cents), 0)::bigint,
  COUNT(*)
FROM payments
WHERE status = 'SUCCESS' AND ($1 = '' OR tenant_id::text = $1)`

func (s *Store) periodTotals(ctx ctxT, tenantID string) (PeriodTotals, error) {
	today, week, month := s.periodStarts()
	var v [12]int64
	err := s.DB.QueryRow(ctx, totalsQuery, tenantID, today, week, month).Scan(
		&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7], &v[8], &v[9], &v[10], &v[11])
	if err != nil {
		return PeriodTotals{}, fmt.Errorf("period totals: %w", err)
	}
	return PeriodTotals{
		Today: totals(v[0], v[1], v[2]),
		Week:  totals(v[3], v[4], v[5]),
		Month: totals(v[6], v[7], v[8]),
		All:   totals(v[9], v[10], v[11]),
	}, nil
}

// dailyQuery returns the last 14 local days, zero-filled. $1 tenant id (” = all), $2 time zone.
const dailyQuery = `
SELECT to_char(d, 'YYYY-MM-DD'),
       COALESCE(p.gross, 0)::bigint, COALESCE(p.fee, 0)::bigint, COALESCE(p.sales, 0)::bigint
FROM generate_series(((NOW() AT TIME ZONE $2)::date - 13)::timestamp, (NOW() AT TIME ZONE $2)::date::timestamp, interval '1 day') AS d
LEFT JOIN (
    SELECT (created_at AT TIME ZONE $2)::date AS day,
           SUM(amount_cents) AS gross, SUM(fee_cents) AS fee, COUNT(*) AS sales
    FROM payments
    WHERE status = 'SUCCESS' AND created_at >= NOW() - interval '16 days'
      AND ($1 = '' OR tenant_id::text = $1)
    GROUP BY 1
) p ON p.day = d::date
ORDER BY d`

func (s *Store) daily(ctx ctxT, tenantID string) ([]DayPoint, error) {
	rows, err := s.DB.Query(ctx, dailyQuery, tenantID, s.TZ)
	if err != nil {
		return nil, fmt.Errorf("daily series: %w", err)
	}
	defer rows.Close()
	out := []DayPoint{}
	for rows.Next() {
		var p DayPoint
		if err := rows.Scan(&p.Date, &p.GrossCents, &p.FeeCents, &p.Sales); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// liveCounts are the "right now" numbers. $1 tenant id (” = all).
const liveQuery = `
SELECT
  (SELECT COUNT(*) FROM sessions WHERE status = 'ACTIVE' AND expires_at > NOW() AND ($1 = '' OR tenant_id::text = $1)),
  (SELECT COUNT(DISTINCT a.username) FROM radacct a JOIN sessions s ON s.username = a.username
     WHERE a.acctstoptime IS NULL AND COALESCE(a.acctupdatetime, a.acctstarttime) >= ` + onlineWindowSQL + `
       AND ($1 = '' OR s.tenant_id::text = $1)),
  (SELECT COUNT(*) FROM devices WHERE $1 = '' OR tenant_id::text = $1),
  (SELECT COUNT(*) FROM devices WHERE last_seen_at >= NOW() - interval '3 minutes' AND ($1 = '' OR tenant_id::text = $1))`

// Live is the "right now" block of an overview.
type Live struct {
	ActivePasses  int64 `json:"active_passes"`
	OnlineNow     int64 `json:"online_now"`
	Routers       int64 `json:"routers"`
	RoutersOnline int64 `json:"routers_online"`
}

func (s *Store) live(ctx ctxT, tenantID string) (Live, error) {
	var l Live
	if err := s.DB.QueryRow(ctx, liveQuery, tenantID).Scan(&l.ActivePasses, &l.OnlineNow, &l.Routers, &l.RoutersOnline); err != nil {
		return l, fmt.Errorf("live counts: %w", err)
	}
	return l, nil
}
