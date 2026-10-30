// Command seed fills a DEVELOPMENT database with believable demo data so the
// dashboards have something to show: two businesses, plans, routers, two weeks
// of sales and a few live customers.
//
//	make seed-demo
//
// It refuses to run unless APP_ENV=development, and it is safe to run twice.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/config"
	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/database"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	demoPassword = "demo-password-123"
	adminEmail   = "admin@demo.local"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Env != "development" {
		return errors.New("refusing to seed demo data unless APP_ENV=development")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := database.Migrate(ctx, db, migrations.Files, slog.Default()); err != nil {
		return err
	}

	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*)::int FROM users WHERE email LIKE '%@demo.local'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		fmt.Println("Demo data already exists - nothing to do. (Sign in with the accounts below.)")
		printAccounts()
		return nil
	}

	store, err := dashboard.NewStore(db, cfg.Timezone)
	if err != nil {
		return err
	}
	if _, err := auth.Create(ctx, db, "", adminEmail, "Demo Admin", demoPassword, auth.RoleAdmin); err != nil {
		return err
	}

	for _, b := range []struct {
		name, sub, owner, email string
		bps                     int
		scale                   int
	}{
		{"Kamau Cyber Cafe", "kamau", "Jane Kamau", "jane@demo.local", 1000, 14},
		{"Moi Hostel WiFi", "moihostel", "Peter Otieno", "peter@demo.local", 800, 9},
	} {
		created, err := store.CreateTenant(ctx, dashboard.NewTenant{
			Name: b.name, Subdomain: b.sub, OwnerName: b.owner, OwnerEmail: b.email, CommissionBPS: b.bps, SeedPlans: true,
		})
		if err != nil {
			return err
		}
		// CreateTenant made a random password; use a known one for the demo.
		uid := ""
		if err := db.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, b.email).Scan(&uid); err != nil {
			return err
		}
		if err := auth.SetPassword(ctx, db, uid, demoPassword); err != nil {
			return err
		}
		tid := created.Tenant.ID
		if err := seedTenant(ctx, db, tid, b.sub, b.bps, b.scale); err != nil {
			return err
		}
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO announcements (tenant_id, message, expires_at)
		SELECT id, 'Free 30-minute pass for every pass bought this weekend!', NOW() + interval '2 days' FROM tenants WHERE subdomain = 'kamau'`); err != nil {
		return err
	}
	fmt.Println("Demo data created.")
	printAccounts()
	return nil
}

func printAccounts() {
	fmt.Println()
	fmt.Println("  Admin   :", adminEmail, "/", demoPassword, " -> /admin")
	fmt.Println("  Owner 1 : jane@demo.local   /", demoPassword, " -> /dashboard   (portal: /?tenant=kamau)")
	fmt.Println("  Owner 2 : peter@demo.local  /", demoPassword, " -> /dashboard   (portal: /?tenant=moihostel)")
	fmt.Println()
}

var extraPlan = plan.Input{Name: "Weekly Unlimited", PriceKES: 500, DurationMinutes: 7 * 1440}

func seedTenant(ctx context.Context, db *pgxpool.Pool, tid, sub string, bps, perDay int) error {
	cap500 := int64(500)
	for _, in := range []plan.Input{
		extraPlan,
		{Name: "Quick 30 Min", PriceKES: 10, DurationMinutes: 30, DataLimitMB: &cap500, SpeedLimit: ptr("2M/4M")},
	} {
		if _, err := plan.Create(ctx, db, tid, in); err != nil {
			return err
		}
	}
	plans, err := plan.ListByTenant(ctx, db, tid)
	if err != nil {
		return err
	}

	// A router that is online right now, and one that has been offline for a day.
	d1, err := device.Register(ctx, db, tid, "Main shop", "48:8f:5a:"+hex2()+":"+hex2()+":"+hex2(), ptr("10.77.0."+strconv.Itoa(10+rand.IntN(40))))
	if err != nil {
		return err
	}
	if err := device.Heartbeat(ctx, db, d1.ID, d1.RadiusSecret); err != nil {
		return err
	}
	d2, err := device.Register(ctx, db, tid, "Back room", "48:8f:5a:"+hex2()+":"+hex2()+":"+hex2(), nil)
	if err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `UPDATE devices SET last_seen_at = NOW() - interval '1 day' WHERE id = $1`, d2.ID); err != nil {
		return err
	}

	// Two weeks of history, weighted to cheaper plans, with a few failed payments.
	r := rand.New(rand.NewPCG(uint64(len(sub)), 42))
	for day := 13; day >= 0; day-- {
		count := perDay/2 + r.IntN(perDay)
		for i := 0; i < count; i++ {
			p := plans[r.IntN(len(plans))]
			if r.IntN(3) > 0 && len(plans) > 1 {
				p = plans[r.IntN(len(plans)-1)]
			}
			at := time.Now().Add(-time.Duration(day)*24*time.Hour - time.Duration(r.IntN(14*3600))*time.Second)
			if at.After(time.Now()) {
				at = time.Now().Add(-time.Duration(r.IntN(3600)) * time.Second)
			}
			status := "SUCCESS"
			if r.IntN(12) == 0 {
				status = "FAILED"
			}
			if err := insertSale(ctx, db, tid, p, bps, status, at, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertSale(ctx context.Context, db *pgxpool.Pool, tid string, p plan.Plan, bps int, status string, at time.Time, r *rand.Rand) error {
	phone := fmt.Sprintf("07%02d%06d", 10+r.IntN(30), r.IntN(1000000))
	ref := fmt.Sprintf("DEMO-%d-%06d", at.Unix(), r.IntN(1000000))
	fee := 0
	var receipt *string
	desc := "The service request is processed successfully."
	if status == "SUCCESS" {
		fee = p.PriceCents * bps / 10000
		rc := fmt.Sprintf("DM%08X", r.Uint32())
		receipt = &rc
	} else {
		desc = "Request cancelled by user"
	}
	var payID string
	if err := db.QueryRow(ctx, `
		INSERT INTO payments (tenant_id, plan_id, phone_number, amount_cents, external_reference, status, mpesa_receipt, result_desc, fee_cents, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) RETURNING id::text`,
		tid, p.ID, phone, p.PriceCents, ref, status, receipt, desc, fee, at).Scan(&payID); err != nil {
		return err
	}
	if status != "SUCCESS" {
		return nil
	}
	// Passes bought in the past are already over; recent ones may still be live.
	s, err := session.Provision(ctx, db, session.Params{
		TenantID: tid, PlanID: p.ID, PaymentID: payID, Phone: phone, DurationMinutes: p.DurationMinutes,
		SpeedLimit: p.SpeedLimit, DataLimitBytes: p.DataLimitBytes,
	})
	if err != nil {
		return err
	}
	end := at.Add(time.Duration(p.DurationMinutes) * time.Minute)
	live := end.After(time.Now())
	st := "EXPIRED"
	if live {
		st = "ACTIVE"
	}
	if _, err := db.Exec(ctx, `UPDATE sessions SET starts_at = $2, expires_at = $3, status = $4, created_at = $2 WHERE id = $1`, s.ID, at, end, st); err != nil {
		return err
	}
	if !live {
		return nil
	}
	// Some live passes are connected right now, with some usage.
	if r.IntN(2) == 0 {
		used := int64(r.IntN(120)) << 20
		_, err = db.Exec(ctx, `
			INSERT INTO radacct (acctsessionid, acctuniqueid, username, nasipaddress, acctstarttime, acctupdatetime, acctinputoctets, acctoutputoctets)
			VALUES ($1, $1, $2, '10.77.0.10', NOW() - interval '10 minutes', NOW() - interval '30 seconds', $3, $4)`,
			ref, s.Username, used/4, used)
		return err
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func hex2() string { return fmt.Sprintf("%02x", rand.IntN(256)) }
