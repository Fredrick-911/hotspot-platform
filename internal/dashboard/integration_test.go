//go:build integration

// Runs the dashboard queries, session provisioning, reconnect, revoke and the
// reaper against a REAL PostgreSQL (with FreeRADIUS's tables loaded).
//
//	make test-api        (needs `make radius-setup` first)
package dashboard_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/database"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func connect(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (use `make test-api`)")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.Migrate(ctx, db, migrations.Files, quiet); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Running it a second time must be a no-op.
	if err := database.Migrate(ctx, db, migrations.Files, quiet); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	return db, ctx
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDashboardsEndToEnd(t *testing.T) {
	db, ctx := connect(t)
	store, err := dashboard.NewStore(db, "Africa/Nairobi")
	must(t, err)

	// ---- onboarding: tenant + owner + starter plans ----
	sub := "it" + strings.ToLower(session.GeneratePIN()) + time.Now().Format("150405")
	created, err := store.CreateTenant(ctx, dashboard.NewTenant{
		Name: "Integration Wifi", Subdomain: sub, OwnerEmail: sub + "@example.com", OwnerName: "Owner", CommissionBPS: 1000, SeedPlans: true,
	})
	must(t, err)
	tid := created.Tenant.ID
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM radcheck WHERE username IN (SELECT username FROM sessions WHERE tenant_id = $1)`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM radreply WHERE username IN (SELECT username FROM sessions WHERE tenant_id = $1)`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM radacct WHERE username IN (SELECT username FROM sessions WHERE tenant_id = $1)`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM sessions WHERE tenant_id = $1`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM payments WHERE tenant_id = $1`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM nas WHERE shortname IN (SELECT 'hp-' || substr(id::text, 1, 8) FROM devices WHERE tenant_id = $1)`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM plans WHERE tenant_id = $1`, tid)
		_, _ = db.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tid) // users, devices, announcements cascade
	})

	owner, err := auth.Authenticate(ctx, db, created.OwnerEmail, created.TempPassword)
	if err != nil || owner.Role != auth.RoleOwner || owner.TenantID == nil || *owner.TenantID != tid {
		t.Fatalf("owner login failed: %+v %v", owner, err)
	}
	if _, err := auth.Authenticate(ctx, db, created.OwnerEmail, "wrong-password-1"); err != auth.ErrInvalidLogin {
		t.Fatalf("wrong password err = %v", err)
	}
	if _, err := store.CreateTenant(ctx, dashboard.NewTenant{Name: "Dup", Subdomain: sub, OwnerEmail: "dup@example.com", CommissionBPS: 1000}); err == nil {
		t.Fatal("duplicate subdomain must be rejected")
	}

	plans, err := store.Plans(ctx, tid)
	must(t, err)
	if len(plans) != 3 {
		t.Fatalf("starter plans = %d, want 3", len(plans))
	}

	// ---- plans: create (500 MB, speed), edit, archive ----
	mb := int64(500)
	speed := "2M/5M"
	dataPlan, err := plan.Create(ctx, db, tid, plan.Input{Name: "500 MB", PriceKES: 50, DurationMinutes: 1440, DataLimitMB: &mb, SpeedLimit: &speed})
	must(t, err)
	if dataPlan.DataLimitBytes == nil || *dataPlan.DataLimitBytes != 500<<20 {
		t.Fatalf("data limit = %v", dataPlan.DataLimitBytes)
	}
	if _, err := plan.Update(ctx, db, tid, dataPlan.ID, plan.Input{Name: "500 MB Daily", PriceKES: 60, DurationMinutes: 1440, DataLimitMB: &mb, SpeedLimit: &speed}); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Update(ctx, db, "00000000-0000-0000-0000-000000000000", dataPlan.ID, plan.Input{Name: "x", PriceKES: 1, DurationMinutes: 1}); err != plan.ErrNotFound {
		t.Fatalf("another tenant must not edit this plan, err = %v", err)
	}
	hourPlan := plans[0]
	must(t, plan.SetActive(ctx, db, tid, hourPlan.ID, false))
	buyable, err := plan.ListByTenant(ctx, db, tid)
	must(t, err)
	if len(buyable) != 3 { // 3 starters + 1 new - 1 archived
		t.Fatalf("buyable plans = %d, want 3", len(buyable))
	}
	must(t, plan.SetActive(ctx, db, tid, hourPlan.ID, true))

	// ---- payments + provisioning ----
	addPayment := func(planID, phone, status string, cents int, ref string) string {
		var id string
		fee := 0
		if status == "SUCCESS" {
			fee = cents / 10
		}
		must(t, db.QueryRow(ctx, `
			INSERT INTO payments (tenant_id, plan_id, phone_number, amount_cents, external_reference, status, fee_cents, mpesa_receipt)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'RCPT' || $5) RETURNING id::text`,
			tid, planID, phone, cents, ref+sub, status, fee).Scan(&id))
		return id
	}
	pay1 := addPayment(dataPlan.ID, "0711000001", "SUCCESS", 6000, "R1")
	addPayment(hourPlan.ID, "0711000002", "SUCCESS", 2000, "R2")
	addPayment(hourPlan.ID, "0711000003", "FAILED", 2000, "R3")
	addPayment(hourPlan.ID, "0711000004", "PENDING", 2000, "R4")

	sess, err := session.Provision(ctx, db, session.Params{
		TenantID: tid, PlanID: dataPlan.ID, PaymentID: pay1, Phone: "0711000001",
		DurationMinutes: 1440, SpeedLimit: dataPlan.SpeedLimit, DataLimitBytes: dataPlan.DataLimitBytes,
	})
	must(t, err)
	again, err := session.Provision(ctx, db, session.Params{TenantID: tid, PlanID: dataPlan.ID, PaymentID: pay1, Phone: "0711000001", DurationMinutes: 1440})
	if err != nil || again.ID != sess.ID || again.Username != sess.Username {
		t.Fatalf("provisioning twice must return the same session: %+v %v", again, err)
	}

	attrs := map[string]string{}
	rows, err := db.Query(ctx, `SELECT attribute, value FROM radcheck WHERE username = $1 UNION ALL SELECT 'reply:' || attribute, value FROM radreply WHERE username = $1`, sess.Username)
	must(t, err)
	for rows.Next() {
		var a, v string
		must(t, rows.Scan(&a, &v))
		attrs[a] = v
	}
	rows.Close()
	if attrs["Cleartext-Password"] != sess.Password || attrs["Max-Total-Octets"] != "524288000" ||
		attrs["reply:Mikrotik-Rate-Limit"] != "2M/5M" || attrs["reply:Acct-Interim-Interval"] != "60" {
		t.Fatalf("radius rows = %v", attrs)
	}
	if _, bad := attrs["reply:Session-Timeout"]; bad {
		t.Fatal("Session-Timeout must NOT be written: FreeRADIUS works out the time left itself")
	}
	if _, bad := attrs["reply:Mikrotik-Total-Limit"]; bad {
		t.Fatal("Mikrotik-Total-Limit must NOT be written: FreeRADIUS works out the data left itself")
	}
	want := time.Now().Add(1440 * time.Minute).Unix()
	if got := atoi(attrs["Expiration"]); got < want-5 || got > want+5 {
		t.Fatalf("Expiration = %v, want about %d (unix seconds)", attrs["Expiration"], want)
	}

	// ---- router + usage ----
	ip := "10.77.0.2"
	dev, err := device.Register(ctx, db, tid, "Test shop", "AA:BB:CC:00:11:22", &ip)
	if err != nil {
		t.Skipf("device register (needs FreeRADIUS nas table): %v", err)
	}
	var nasCount int
	must(t, db.QueryRow(ctx, `SELECT COUNT(*)::int FROM nas WHERE nasname = $1 AND secret = $2`, ip, dev.RadiusSecret).Scan(&nasCount))
	if nasCount != 1 {
		t.Fatalf("FreeRADIUS nas rows for the router = %d, want 1", nasCount)
	}
	if _, err := device.Register(ctx, db, tid, "dup", "aa:bb:cc:00:11:22", nil); err != device.ErrDuplicateMAC {
		t.Fatalf("duplicate MAC err = %v", err)
	}
	must(t, device.Heartbeat(ctx, db, dev.ID, dev.RadiusSecret))
	if err := device.Heartbeat(ctx, db, dev.ID, "wrong"); err != device.ErrDeviceNotFound {
		t.Fatalf("heartbeat with a wrong secret err = %v", err)
	}

	addAcct := func(id string, in, out int64, open bool, ago string) {
		stop := "now()"
		if open {
			stop = "NULL"
		}
		_, err := db.Exec(ctx, `INSERT INTO radacct (acctsessionid, acctuniqueid, username, nasipaddress, acctstarttime, acctupdatetime, acctstoptime, acctinputoctets, acctoutputoctets)
			VALUES ($1, $1, $2, $3::inet, now() - $4::interval, now(), `+stop+`, $5, $6)`, id, sess.Username, ip, ago, in, out)
		must(t, err)
	}
	addAcct(sess.Username+"-a", 50<<20, 100<<20, true, "10 minutes")

	// ---- overview ----
	ov, err := store.TenantOverview(ctx, tid)
	must(t, err)
	if ov.Today.GrossCents != 8000 || ov.Today.FeeCents != 800 || ov.Today.NetCents != 7200 || ov.Today.Sales != 2 {
		t.Fatalf("today = %+v", ov.Today)
	}
	if ov.Live.OnlineNow != 1 || ov.Live.ActivePasses != 1 || ov.Live.Routers != 1 || ov.Live.RoutersOnline != 1 {
		t.Fatalf("live = %+v", ov.Live)
	}
	if len(ov.Daily) != 14 || ov.Daily[13].GrossCents != 8000 || ov.Daily[0].GrossCents != 0 {
		t.Fatalf("daily = %+v", ov.Daily)
	}
	if ov.DataUsed30dBytes != 150<<20 || len(ov.TopPlans) != 2 || ov.TopPlans[0].Name != "500 MB Daily" {
		t.Fatalf("data=%d top=%+v", ov.DataUsed30dBytes, ov.TopPlans)
	}

	// ---- lists ----
	pays, err := store.Payments(ctx, tid, "", 50)
	must(t, err)
	if len(pays) != 4 {
		t.Fatalf("payments = %d", len(pays))
	}
	if ok, _ := store.Payments(ctx, tid, "success", 50); len(ok) != 2 {
		t.Fatalf("SUCCESS filter = %d", len(ok))
	}
	cust, err := store.Customers(ctx, tid, "0711", 50)
	must(t, err)
	if len(cust) != 1 || cust[0].State != "ONLINE" || cust[0].DataUsedBytes != 150<<20 {
		t.Fatalf("customers = %+v", cust)
	}
	devs, err := store.Devices(ctx, tid)
	must(t, err)
	if len(devs) != 1 || !devs[0].Online || devs[0].ActiveUsers != 1 || devs[0].Name != "Test shop" {
		t.Fatalf("devices = %+v", devs)
	}

	// ---- customer account ----
	acc, err := store.Account(ctx, tid, sess.ID)
	must(t, err)
	if acc.State != "ONLINE" || acc.SecondsLeft < 86300 || acc.DataLeftBytes == nil || *acc.DataLeftBytes != 350<<20 || len(acc.Receipts) != 1 {
		t.Fatalf("account = %+v", acc)
	}

	// ---- announcements ----
	ann, err := store.CreateAnnouncement(ctx, tid, owner.ID, "Router restarts at 2pm", time.Hour)
	must(t, err)
	if list, _ := store.Announcements(ctx, tid, true); len(list) != 1 {
		t.Fatalf("announcements = %d", len(list))
	}
	if _, err := store.CreateAnnouncement(ctx, tid, owner.ID, "   ", time.Hour); err != dashboard.ErrBadAnnouncement {
		t.Fatalf("empty announcement err = %v", err)
	}
	must(t, store.DeleteAnnouncement(ctx, tid, ann.ID))

	// ---- reconnect rules ----
	if _, err := session.Reconnect(ctx, db, tid, "0711000001", "0000", nil); err != session.ErrNoActiveSession {
		t.Fatalf("wrong PIN err = %v", err)
	}
	if _, err := session.Reconnect(ctx, db, tid, "0799999999", sess.Password, nil); err != session.ErrNoActiveSession {
		t.Fatalf("wrong phone err = %v (a PIN alone must not log anyone in)", err)
	}
	if got, err := session.Reconnect(ctx, db, tid, "0711000001", sess.Password, nil); err != nil || got.ID != sess.ID {
		t.Fatalf("right phone+PIN: %+v %v", got, err)
	}
	addAcct(sess.Username+"-b", 100<<20, 300<<20, false, "5 minutes") // now 550 MB > 500 MB
	if _, err := session.Reconnect(ctx, db, tid, "0711000001", sess.Password, nil); err != session.ErrDataExhausted {
		t.Fatalf("exhausted pass err = %v", err)
	}
	if c, _ := store.Customers(ctx, tid, "", 10); c[0].State != "DATA_FINISHED" {
		t.Fatalf("state after using all data = %s", c[0].State)
	}

	// ---- reaper: the open session is over its data, so it must be disconnected ----
	fake := &recorder{}
	r := &session.Reaper{DB: db, Disconnector: fake, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	st, err := r.RunOnce(ctx)
	must(t, err)
	if st.Disconnected < 1 || !fake.saw(sess.Username) {
		t.Fatalf("reaper stats %+v, disconnected %v", st, fake.users)
	}

	// ---- revoke ----
	revoked, err := session.Revoke(ctx, db, tid, sess.ID)
	must(t, err)
	if revoked.Status != "REVOKED" {
		t.Fatal("not revoked")
	}
	var left int
	must(t, db.QueryRow(ctx, `SELECT COUNT(*)::int FROM radcheck WHERE username = $1`, sess.Username).Scan(&left))
	if left != 0 {
		t.Fatalf("radcheck rows left after revoke = %d", left)
	}
	if _, err := session.Revoke(ctx, db, tid, sess.ID); err != session.ErrNoActiveSession {
		t.Fatalf("second revoke err = %v", err)
	}

	// ---- admin ----
	ao, err := store.AdminOverview(ctx)
	must(t, err)
	if ao.Tenants.Total < 1 || ao.All.GrossCents < 8000 || len(ao.Daily) != 14 {
		t.Fatalf("admin overview = %+v", ao)
	}
	ts, err := store.Tenants(ctx)
	must(t, err)
	var mine *dashboard.TenantRow
	for i := range ts {
		if ts[i].ID == tid {
			mine = &ts[i]
		}
	}
	if mine == nil || mine.Gross30d != 8000 || mine.Fee30d != 800 || mine.Routers != 1 || mine.OwnerEmail == nil || mine.Plans != 4 {
		t.Fatalf("tenant row = %+v", mine)
	}
	suspended, bps := "SUSPENDED", 1500
	must(t, store.UpdateTenant(ctx, tid, &suspended, &bps))
	if ts, _ = store.Tenants(ctx); findTenant(ts, tid).Status != "SUSPENDED" || findTenant(ts, tid).CommissionBPS != 1500 {
		t.Fatal("tenant update not applied")
	}
	email, pw, err := store.ResetOwnerPassword(ctx, tid)
	must(t, err)
	if _, err := auth.Authenticate(ctx, db, email, pw); err != nil {
		t.Fatalf("reset password does not work: %v", err)
	}
	if ap, _ := store.AdminPayments(ctx, tid, "FAILED", 10); len(ap) != 1 {
		t.Fatalf("admin payments = %d", len(ap))
	}
	if ad, _ := store.AdminDevices(ctx); len(ad) < 1 {
		t.Fatal("admin devices empty")
	}

	// ---- router update/delete keep FreeRADIUS's nas table in sync ----
	if _, err := device.Update(ctx, db, tid, dev.ID, "Renamed", "10.77.0.9"); err != nil {
		t.Fatal(err)
	}
	must(t, db.QueryRow(ctx, `SELECT COUNT(*)::int FROM nas WHERE nasname = '10.77.0.9'`).Scan(&nasCount))
	if nasCount != 1 {
		t.Fatalf("nas not updated: %d", nasCount)
	}
	must(t, device.Delete(ctx, db, tid, dev.ID))
	must(t, db.QueryRow(ctx, `SELECT COUNT(*)::int FROM nas WHERE nasname IN ('10.77.0.2', '10.77.0.9')`).Scan(&nasCount))
	if nasCount != 0 {
		t.Fatalf("nas rows left after deleting the router: %d", nasCount)
	}
}

type recorder struct{ users []string }

func (r *recorder) Disconnect(_ context.Context, s session.Online) error {
	r.users = append(r.users, s.Username)
	return nil
}
func (r *recorder) saw(u string) bool {
	for _, x := range r.users {
		if x == u {
			return true
		}
	}
	return false
}

func findTenant(ts []dashboard.TenantRow, id string) dashboard.TenantRow {
	for _, t := range ts {
		if t.ID == id {
			return t
		}
	}
	return dashboard.TenantRow{}
}

func atoi(s string) int64 {
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}
