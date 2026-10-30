package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Online is one RADIUS accounting session that is still open.
type Online struct {
	SessionID string // radacct.acctsessionid: the router's own session id
	Username  string
	NASIP     string // the router that holds the session
}

// Disconnector kicks one session off its router.
type Disconnector interface {
	Disconnect(ctx context.Context, s Online) error
}

// OpenAccounting lists the still-open accounting sessions of a username.
func OpenAccounting(ctx context.Context, db *pgxpool.Pool, username string) ([]Online, error) {
	rows, err := db.Query(ctx, `
		SELECT acctsessionid, username, host(nasipaddress)
		FROM radacct WHERE username = $1 AND acctstoptime IS NULL`, username)
	if err != nil {
		return nil, fmt.Errorf("open accounting: %w", err)
	}
	defer rows.Close()
	var out []Online
	for rows.Next() {
		var o Online
		if err := rows.Scan(&o.SessionID, &o.Username, &o.NASIP); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DisconnectUser sends a Disconnect-Request for every open session of username.
// It returns how many were disconnected; failures are joined into the error.
func DisconnectUser(ctx context.Context, db *pgxpool.Pool, d Disconnector, username string) (int, error) {
	open, err := OpenAccounting(ctx, db, username)
	if err != nil {
		return 0, err
	}
	var (
		done int
		errs []error
	)
	for _, o := range open {
		if err := d.Disconnect(ctx, o); err != nil {
			errs = append(errs, fmt.Errorf("%s on %s: %w", o.Username, o.NASIP, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// Reaper is the backstop for pass limits. The MikroTik normally cuts a user
// itself when Session-Timeout or Mikrotik-Total-Limit is reached; the Reaper
// catches any session that is still online afterwards.
type Reaper struct {
	DB           *pgxpool.Pool
	Disconnector Disconnector
	Logger       *slog.Logger

	Interval     time.Duration // how often to look (default 30s)
	Grace        time.Duration // how long past expiry before stepping in (default 30s)
	ActiveWithin time.Duration // ignore sessions silent for this long: they are dead (default 10m)
	Retention    time.Duration // keep an ended pass's RADIUS rows this long (default 7 days)
}

// Stats is what one pass of the Reaper did.
type Stats struct {
	Expired      int // sessions marked EXPIRED
	Disconnected int // online sessions kicked
	Cleaned      int // old RADIUS user rows removed
}

func (r *Reaper) defaults() {
	if r.Interval <= 0 {
		r.Interval = 30 * time.Second
	}
	if r.Grace <= 0 {
		r.Grace = 30 * time.Second
	}
	if r.ActiveWithin <= 0 {
		r.ActiveWithin = 10 * time.Minute
	}
	if r.Retention <= 0 {
		r.Retention = 7 * 24 * time.Hour
	}
	if r.Logger == nil {
		r.Logger = slog.Default()
	}
}

func seconds(d time.Duration) float64 { return d.Seconds() }

// RunOnce makes one pass. A failure on one session is logged and does not stop
// the others; the returned error joins every failure.
func (r *Reaper) RunOnce(ctx context.Context) (Stats, error) {
	r.defaults()
	var (
		st   Stats
		errs []error
	)

	// 1. Mark passes whose time is up.
	rows, err := r.DB.Query(ctx, `
		UPDATE sessions SET status = 'EXPIRED'
		WHERE status = 'ACTIVE' AND expires_at <= NOW() - make_interval(secs => $1)
		RETURNING id::text`, seconds(r.Grace))
	if err != nil {
		return st, fmt.Errorf("expire sessions: %w", err)
	}
	for rows.Next() {
		st.Expired++
	}
	rows.Close()

	// 2. Disconnect anyone still online whose pass is over (time up, revoked, or data used up).
	over, err := r.DB.Query(ctx, `
		SELECT a.acctsessionid, a.username, host(a.nasipaddress)
		FROM radacct a
		JOIN sessions s ON s.username = a.username
		WHERE a.acctstoptime IS NULL
		  AND COALESCE(a.acctupdatetime, a.acctstarttime) >= NOW() - make_interval(secs => $2)
		  AND (
		        s.status IN ('EXPIRED', 'REVOKED')
		     OR s.expires_at <= NOW() - make_interval(secs => $1)
		     OR (s.data_limit_bytes > 0 AND s.data_limit_bytes <=
		         (SELECT COALESCE(SUM(u.acctinputoctets + u.acctoutputoctets), 0)::bigint FROM radacct u WHERE u.username = a.username))
		      )`, seconds(r.Grace), seconds(r.ActiveWithin))
	if err != nil {
		return st, fmt.Errorf("find overdue sessions: %w", err)
	}
	var targets []Online
	for over.Next() {
		var o Online
		if err := over.Scan(&o.SessionID, &o.Username, &o.NASIP); err != nil {
			over.Close()
			return st, err
		}
		targets = append(targets, o)
	}
	over.Close()

	for _, o := range targets {
		if err := r.Disconnector.Disconnect(ctx, o); err != nil {
			r.Logger.Error("disconnect failed", "user", o.Username, "session", o.SessionID, "nas", o.NASIP, "err", err)
			errs = append(errs, err)
			continue
		}
		r.Logger.Info("disconnected overdue session", "user", o.Username, "session", o.SessionID, "nas", o.NASIP)
		st.Disconnected++
	}

	// 3. Remove the RADIUS login rows of passes that ended long ago.
	for _, q := range []string{
		`DELETE FROM radcheck WHERE username IN (SELECT username FROM sessions WHERE status <> 'ACTIVE' AND expires_at < NOW() - make_interval(secs => $1))`,
		`DELETE FROM radreply WHERE username IN (SELECT username FROM sessions WHERE status <> 'ACTIVE' AND expires_at < NOW() - make_interval(secs => $1))`,
	} {
		tag, err := r.DB.Exec(ctx, q, seconds(r.Retention))
		if err != nil {
			errs = append(errs, fmt.Errorf("clean radius rows: %w", err))
			continue
		}
		st.Cleaned += int(tag.RowsAffected())
	}
	return st, errors.Join(errs...)
}

// Run calls RunOnce every Interval until ctx is cancelled.
func (r *Reaper) Run(ctx context.Context) error {
	r.defaults()
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.Logger.Error("reaper pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
