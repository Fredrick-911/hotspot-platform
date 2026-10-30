//go:build integration

// Drives the whole web application against a REAL PostgreSQL: staff login,
// onboarding a business, plans, routers, a customer buying a pass, the PayHero
// webhook, revoking, tenant isolation and live updates.
//
//	make test-api        (needs `make radius-setup` first)
package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/config"
	"github.com/Fredrick-911/hotspot-platform/internal/database"
	"github.com/Fredrick-911/hotspot-platform/migrations"
)

// client is a tiny browser: it keeps cookies between calls.
type client struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	ip      string // pretend source address; default 192.0.2.1
}

type resp struct {
	Code   int
	Body   []byte
	Header http.Header
}

func (r resp) JSON() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body, &m)
	return m
}

func (c *client) do(method, path string, body any, extra ...string) resp {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if method != http.MethodGet {
		req.Header.Set(csrfHeader, csrfValue)
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "" {
			req.Header.Del(extra[i])
		} else {
			req.Header.Set(extra[i], extra[i+1])
		}
	}
	if c.ip != "" {
		req.RemoteAddr = c.ip + ":4321"
	}
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	res := rec.Result()
	for _, ck := range res.Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}
	return resp{Code: rec.Code, Body: rec.Body.Bytes(), Header: res.Header}
}

func (c *client) want(code int, r resp, what string) resp {
	c.t.Helper()
	if r.Code != code {
		c.t.Fatalf("%s: status %d, want %d, body %s", what, r.Code, code, r.Body)
	}
	return r
}

func rnd(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("field %q missing or not a string in %v", key, m)
	}
	return v
}

func num(m map[string]any, path ...string) float64 {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return -1
		}
		cur = mm[p]
	}
	f, _ := cur.(float64)
	return f
}

func TestWebApplicationEndToEnd(t *testing.T) {
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

	cfg := config.Config{
		Env: "development", BaseURL: "http://127.0.0.1:8080", SessionSecret: []byte(strings.Repeat("k", 32)),
		AllowTenantQuery: true, Timezone: "Africa/Nairobi",
	}
	srv := NewServer(db, cfg)
	h := srv.Handler()
	newClient := func() *client { return &client{t: t, h: h, cookies: map[string]string{}} }

	// ---- platform admin ----
	adminEmail := "admin-" + rnd(4) + "@test.local"
	adminPass := "correct-horse-" + rnd(3)
	if _, err := auth.Create(ctx, db, "", adminEmail, "Test Admin", adminPass, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	admin := newClient()
	admin.want(401, admin.do("POST", "/api/auth/login", map[string]string{"email": adminEmail, "password": "wrong"}), "bad password")
	admin.want(403, admin.do("POST", "/api/auth/login", map[string]string{"email": adminEmail, "password": adminPass}, csrfHeader, ""), "login without CSRF header")
	admin.want(200, admin.do("POST", "/api/auth/login", map[string]string{"email": adminEmail, "password": adminPass}), "admin login")
	admin.want(200, admin.do("GET", "/api/admin/overview", nil), "admin overview")
	if r := admin.want(200, admin.do("GET", "/admin", nil), "admin page"); !strings.Contains(string(r.Body), "Platform overview") {
		t.Fatal("admin page did not render")
	}

	// ---- onboard two businesses ----
	subA, subB := "ta"+rnd(3), "tb"+rnd(3)
	mk := func(sub, email string) (string, string) {
		r := admin.want(201, admin.do("POST", "/api/admin/tenants", map[string]any{
			"name": "Biz " + sub, "subdomain": sub, "owner_name": "Owner " + sub, "owner_email": email, "commission_bps": 1000, "seed_plans": false,
		}), "create tenant "+sub).JSON()
		return str(t, r, "temp_password"), str(t, r["tenant"].(map[string]any), "id")
	}
	emailA, emailB := "a-"+rnd(3)+"@test.local", "b-"+rnd(3)+"@test.local"
	passA, idA := mk(subA, emailA)
	passB, _ := mk(subB, emailB)
	admin.want(400, admin.do("POST", "/api/admin/tenants", map[string]any{"name": "Dup", "subdomain": subA, "owner_name": "x", "owner_email": "d-" + rnd(3) + "@test.local", "commission_bps": 1000}), "duplicate subdomain")
	admin.want(404, admin.do("PATCH", "/api/admin/tenants/not-a-uuid", map[string]any{"status": "ACTIVE"}), "bad id")

	ownerA, ownerB := newClient(), newClient()
	ownerA.want(200, ownerA.do("POST", "/api/auth/login", map[string]string{"email": emailA, "password": passA}), "owner A login")
	ownerB.want(200, ownerB.do("POST", "/api/auth/login", map[string]string{"email": emailB, "password": passB}), "owner B login")
	ownerA.want(403, ownerA.do("GET", "/api/admin/overview", nil), "owner must not reach admin API")
	if r := ownerA.do("GET", "/admin", nil); r.Code != 303 {
		t.Fatalf("owner opening /admin: %d, want redirect", r.Code)
	}
	if r := ownerA.want(200, ownerA.do("GET", "/dashboard", nil), "owner page"); !strings.Contains(string(r.Body), "Biz "+subA) {
		t.Fatal("tenant page did not show the business name")
	}

	// ---- plans ----
	ownerA.want(400, ownerA.do("POST", "/api/tenant/plans", map[string]any{"name": "", "price_kes": 50, "duration_minutes": 60}), "invalid plan")
	ownerA.want(400, ownerA.do("POST", "/api/tenant/plans", map[string]any{"name": "Bad speed", "price_kes": 50, "duration_minutes": 60, "speed_limit": "fast"}), "invalid speed")
	planA := ownerA.want(201, ownerA.do("POST", "/api/tenant/plans", map[string]any{
		"name": "1 Hour <b>", "price_kes": 50, "duration_minutes": 60, "data_limit_mb": 100, "speed_limit": "2M/5M",
	}), "create plan").JSON()
	planID := str(t, planA, "id")
	ownerB.want(404, ownerB.do("PUT", "/api/tenant/plans/"+planID, map[string]any{"name": "Hijack", "price_kes": 1, "duration_minutes": 1}), "other tenant edits plan")
	ownerB.want(404, ownerB.do("POST", "/api/tenant/plans/"+planID+"/archive", nil), "other tenant archives plan")

	// ---- router + heartbeat ----
	mac := "48:8f:5a:" + rnd(1) + ":" + rnd(1) + ":" + rnd(1)
	dev := ownerA.want(201, ownerA.do("POST", "/api/tenant/devices", map[string]any{"name": "Main shop", "mac_address": mac, "nas_ip_address": "10.77.0.2"}), "register router").JSON()
	script := str(t, dev, "script")
	devID := str(t, dev, "device")
	if !strings.Contains(script, "/radius add") || !strings.Contains(script, "/api/device/heartbeat?id="+devID) {
		t.Fatalf("script lacks radius or heartbeat lines:\n%s", script)
	}
	secret := script[strings.Index(script, `secret="`)+8:]
	secret = secret[:strings.Index(secret, `"`)]
	anon := newClient()
	anon.want(400, anon.do("POST", "/api/device/heartbeat?id="+devID, nil), "heartbeat without key")
	hb := httptest.NewRequest("POST", "/api/device/heartbeat?id="+devID, strings.NewReader("key=wrong"))
	hb.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, hb)
	if rec.Code != 403 {
		t.Fatalf("heartbeat wrong key: %d", rec.Code)
	}
	hb = httptest.NewRequest("POST", "/api/device/heartbeat?id="+devID, strings.NewReader("key="+secret))
	hb.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hb)
	if rec.Code != 204 {
		t.Fatalf("heartbeat right key: %d %s", rec.Code, rec.Body)
	}
	devs := ownerA.want(200, ownerA.do("GET", "/api/tenant/devices", nil), "list routers").JSON()["devices"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["online"] != true {
		t.Fatalf("router should be online after a heartbeat: %v", devs)
	}
	if strings.Contains(string(ownerA.do("GET", "/api/tenant/devices", nil).Body), secret) {
		t.Fatal("device list leaks the RADIUS secret")
	}

	// ---- live updates over SSE ----
	ts := httptest.NewServer(h)
	defer ts.Close()
	sseReq, _ := http.NewRequest("GET", ts.URL+"/api/tenant/stream", nil)
	sseReq.AddCookie(&http.Cookie{Name: cookieStaff, Value: ownerA.cookies[cookieStaff]})
	sseCtx, cancelSSE := context.WithTimeout(ctx, 20*time.Second)
	defer cancelSSE()
	sseRes, err := http.DefaultClient.Do(sseReq.WithContext(sseCtx))
	if err != nil || sseRes.StatusCode != 200 {
		t.Fatalf("open stream: %v %v", err, sseRes)
	}
	defer sseRes.Body.Close()
	gotSale := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(sseRes.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "event: sale") {
				gotSale <- line
				return
			}
		}
	}()

	// ---- customer buys a pass (development payment shortcut) ----
	cust := newClient()
	q := "?tenant=" + subA
	if r := cust.want(200, cust.do("GET", "/"+q, nil), "portal"); !strings.Contains(string(r.Body), "1 Hour &lt;b&gt;") {
		t.Fatalf("portal must show the plan with its name escaped, got:\n%s", r.Body)
	}
	cust.want(400, cust.do("POST", "/api/payments/initiate"+q, map[string]string{"plan_id": planID, "phone_number": "12345"}), "bad phone")
	cust.want(400, cust.do("POST", "/api/payments/initiate"+q, map[string]string{"plan_id": "nope", "phone_number": "0712345678"}), "bad plan id")
	cust2 := newClient()
	cust2.want(400, cust2.do("POST", "/api/payments/initiate?tenant="+subB, map[string]string{"plan_id": planID, "phone_number": "0722000111"}), "plan of another tenant")

	phone := "07" + "11" + rnd(3)[:6]
	for _, c := range phone[2:] { // phone digits only
		if c < '0' || c > '9' {
			phone = "0712345678"
			break
		}
	}
	init := cust.want(202, cust.do("POST", "/api/payments/initiate"+q, map[string]string{"plan_id": planID, "phone_number": phone, "mac_address": "AA:BB:CC:00:11:22"}), "initiate").JSON()
	payID := str(t, init, "payment_id")
	if init["dev_mode"] != true {
		t.Fatalf("expected dev mode: %v", init)
	}
	if st := cust.want(200, cust.do("GET", "/api/payments/status"+q+"&id="+payID, nil), "status pending").JSON(); st["status"] != "PENDING" {
		t.Fatalf("status = %v", st)
	}
	cust2.want(404, cust2.do("GET", "/api/payments/status?tenant="+subB+"&id="+payID, nil), "other tenant polls payment")
	cust.want(200, cust.do("POST", "/api/dev/confirm"+q, map[string]string{"payment_id": payID}), "dev confirm")
	cust.want(200, cust.do("POST", "/api/dev/confirm"+q, map[string]string{"payment_id": payID}), "dev confirm twice")
	st := cust.want(200, cust.do("GET", "/api/payments/status"+q+"&id="+payID, nil), "status success").JSON()
	user, pin := str(t, st, "username"), str(t, st, "pin")
	if st["status"] != "SUCCESS" || !strings.HasPrefix(user, phone+"-") || len(pin) != 4 {
		t.Fatalf("status = %v", st)
	}
	if cust.cookies[cookieCustomer] == "" {
		t.Fatal("customer cookie not set after payment")
	}

	var sessions, fee int
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.QueryRow(ctx, `SELECT COUNT(*)::int FROM sessions WHERE payment_id = $1`, payID).Scan(&sessions))
	must(db.QueryRow(ctx, `SELECT fee_cents FROM payments WHERE id = $1`, payID).Scan(&fee))
	if sessions != 1 || fee != 500 {
		t.Fatalf("sessions=%d fee=%d, want 1 and 500 (10%% of KES 50)", sessions, fee)
	}
	var radRows int
	must(db.QueryRow(ctx, `SELECT COUNT(*)::int FROM radcheck WHERE username = $1 AND attribute IN ('Cleartext-Password','Expiration','Max-Total-Octets')`, user).Scan(&radRows))
	if radRows != 3 {
		t.Fatalf("expected password+expiry+data rows in radcheck, got %d", radRows)
	}
	select {
	case <-gotSale:
	case <-time.After(5 * time.Second):
		t.Fatal("owner's live stream never received the sale event")
	}

	// ---- owner and admin see the money ----
	ov := ownerA.want(200, ownerA.do("GET", "/api/tenant/overview", nil), "owner overview").JSON()
	if num(ov, "today", "gross_cents") != 5000 || num(ov, "today", "fee_cents") != 500 || num(ov, "today", "net_cents") != 4500 {
		t.Fatalf("owner today totals wrong: %v", ov["today"])
	}
	ovB := ownerB.want(200, ownerB.do("GET", "/api/tenant/overview", nil), "owner B overview").JSON()
	if num(ovB, "all", "sales") != 0 {
		t.Fatalf("owner B sees owner A's sales: %v", ovB["all"])
	}
	ao := admin.want(200, admin.do("GET", "/api/admin/overview", nil), "admin overview").JSON()
	if num(ao, "all", "fee_cents") < 500 {
		t.Fatalf("admin fee total too small: %v", ao["all"])
	}

	// ---- customer dashboard ----
	me := cust.want(200, cust.do("GET", "/api/customer/me"+q, nil), "customer me").JSON()
	if me["state"] != "ACTIVE" || me["pin"] != pin || num(me, "data_limit_bytes") != 100<<20 {
		t.Fatalf("customer view: %v", me)
	}
	cust.want(401, cust.do("GET", "/api/customer/me?tenant="+subB, nil), "customer cookie must not open another business")
	newClient().want(401, newClient().do("GET", "/api/customer/me"+q, nil), "no cookie")

	// ---- reconnect: phone AND pin, with guess limiting ----
	other := newClient()
	other.want(404, other.do("POST", "/api/sessions/reconnect"+q, map[string]string{"phone": phone, "pin": "0000x"[:4]}), "wrong pin")
	rc := other.want(200, other.do("POST", "/api/sessions/reconnect"+q, map[string]string{"phone": phone, "pin": pin, "mac_address": "AA:BB:CC:00:11:33"}), "reconnect").JSON()
	if rc["username"] != user {
		t.Fatalf("reconnect returned %v", rc)
	}
	other.want(400, other.do("POST", "/api/sessions/reconnect"+q, map[string]string{"phone": "", "pin": pin}), "pin alone is not enough")
	guesser := newClient()
	guesser.ip = "198.51.100.77"
	limited := false
	for i := 0; i < 12; i++ {
		wrong := "9999"
		if wrong == pin {
			wrong = "9998"
		}
		if guesser.do("POST", "/api/sessions/reconnect"+q, map[string]string{"phone": "0799000000", "pin": wrong}).Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("PIN guessing was never rate limited")
	}

	// ---- PayHero webhook ----
	var ref2, pay2 string
	{
		init2 := cust.want(202, cust.do("POST", "/api/payments/initiate"+q, map[string]string{"plan_id": planID, "phone_number": "0733000111"}), "second payment").JSON()
		pay2 = str(t, init2, "payment_id")
		must(db.QueryRow(ctx, `SELECT external_reference FROM payments WHERE id = $1`, pay2).Scan(&ref2))
	}
	body := func(ref string, amount float64, code int, receipt string) map[string]any {
		return map[string]any{"status": true, "response": map[string]any{
			"ExternalReference": ref, "Amount": amount, "ResultCode": code, "MpesaReceiptNumber": receipt, "ResultDesc": "ok", "Status": "Success"}}
	}
	anon.want(401, anon.do("POST", "/api/payments/callback?t=forged", body(ref2, 50, 0, "RCP1")), "forged webhook")
	good := "/api/payments/callback?t=" + srv.signer.Token(callbackLabel, ref2)
	anon.want(200, anon.do("POST", good, body(ref2, 20, 0, "RCP0")), "underpaid webhook")
	var status string
	must(db.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1`, pay2).Scan(&status))
	if status != "FAILED" {
		t.Fatalf("underpayment should fail the payment, got %s", status)
	}
	init3 := cust.want(202, cust.do("POST", "/api/payments/initiate"+q, map[string]string{"plan_id": planID, "phone_number": "0733000222"}), "third payment").JSON()
	pay3 := str(t, init3, "payment_id")
	var ref3 string
	must(db.QueryRow(ctx, `SELECT external_reference FROM payments WHERE id = $1`, pay3).Scan(&ref3))
	good3 := "/api/payments/callback?t=" + srv.signer.Token(callbackLabel, ref3)
	anon.want(200, anon.do("POST", good3, body(ref3, 50, 0, "RCP3")), "good webhook")
	anon.want(200, anon.do("POST", good3, body(ref3, 50, 0, "RCP3")), "retried webhook")
	must(db.QueryRow(ctx, `SELECT COUNT(*)::int FROM sessions WHERE payment_id = $1`, pay3).Scan(&sessions))
	if sessions != 1 {
		t.Fatalf("webhook retry created %d sessions", sessions)
	}
	anon.want(200, anon.do("POST", good3, map[string]any{"response": map[string]any{"ExternalReference": ref3, "ResultCode": 1032}}), "late failure after success is ignored")
	must(db.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1`, pay3).Scan(&status))
	if status != "SUCCESS" {
		t.Fatalf("a late failure callback must not undo a paid payment, got %s", status)
	}

	// ---- customers table, revoke, isolation ----
	list := ownerA.want(200, ownerA.do("GET", "/api/tenant/customers?q="+phone[:6], nil), "customers").JSON()["customers"].([]any)
	if len(list) != 1 {
		t.Fatalf("customers = %v", list)
	}
	sessID := str(t, list[0].(map[string]any), "session_id")
	if strings.Contains(string(ownerA.do("GET", "/api/tenant/customers", nil).Body), pin) {
		t.Fatal("customers table leaks PINs")
	}
	ownerB.want(404, ownerB.do("POST", "/api/tenant/customers/"+sessID+"/revoke", nil), "other tenant revokes")
	rv := ownerA.want(200, ownerA.do("POST", "/api/tenant/customers/"+sessID+"/revoke", nil), "revoke").JSON()
	if rv["status"] != "revoked" {
		t.Fatalf("revoke = %v", rv)
	}
	ownerA.want(404, ownerA.do("POST", "/api/tenant/customers/"+sessID+"/revoke", nil), "revoke twice")
	must(db.QueryRow(ctx, `SELECT COUNT(*)::int FROM radcheck WHERE username = $1`, user).Scan(&radRows))
	if radRows != 0 {
		t.Fatalf("revoked pass still has %d radcheck rows", radRows)
	}
	if me := cust.want(200, cust.do("GET", "/api/customer/me"+q, nil), "me after revoke").JSON(); me["state"] != "REVOKED" {
		t.Fatalf("state after revoke = %v", me["state"])
	}
	other.want(404, other.do("POST", "/api/sessions/reconnect"+q, map[string]string{"phone": phone, "pin": pin}), "reconnect after revoke")

	// ---- announcements ----
	ownerA.want(400, ownerA.do("POST", "/api/tenant/announcements", map[string]any{"message": "  "}), "empty announcement")
	an := ownerA.want(201, ownerA.do("POST", "/api/tenant/announcements", map[string]any{"message": "Maintenance at 10pm <script>", "hours": 6}), "announce").JSON()
	if r := cust.want(200, cust.do("GET", "/"+q, nil), "portal with banner"); !strings.Contains(string(r.Body), "Maintenance at 10pm &lt;script&gt;") {
		t.Fatal("announcement missing or not escaped on the portal")
	}
	ownerB.want(404, ownerB.do("DELETE", "/api/tenant/announcements/"+str(t, an, "id"), nil), "other tenant deletes announcement ")
	ownerA.want(200, ownerA.do("DELETE", "/api/tenant/announcements/"+str(t, an, "id"), nil), "delete announcement")

	// ---- password change, suspend, logout ----
	ownerA.want(401, ownerA.do("POST", "/api/auth/password", map[string]string{"current": "nope", "new": "a-long-new-password"}), "wrong current password")
	ownerA.want(400, ownerA.do("POST", "/api/auth/password", map[string]string{"current": passA, "new": "short"}), "short new password")
	ownerA.want(200, ownerA.do("POST", "/api/auth/password", map[string]string{"current": passA, "new": "a-long-new-password"}), "change password")

	admin.want(200, admin.do("PATCH", "/api/admin/tenants/"+idA, map[string]any{"status": "SUSPENDED"}), "suspend")
	cust.want(503, cust.do("GET", "/"+q, nil), "suspended portal")
	admin.want(200, admin.do("PATCH", "/api/admin/tenants/"+idA, map[string]any{"status": "ACTIVE", "commission_bps": 2000}), "reactivate")
	rp := admin.want(200, admin.do("POST", "/api/admin/tenants/"+idA+"/reset-password", nil), "reset owner password").JSON()
	fresh := newClient()
	fresh.want(200, fresh.do("POST", "/api/auth/login", map[string]string{"email": emailA, "password": str(t, rp, "temp_password")}), "login with reset password")
	ownerA.want(200, ownerA.do("POST", "/api/auth/logout", nil), "logout")
	ownerA.want(401, ownerA.do("GET", "/api/tenant/overview", nil), "after logout")
}
