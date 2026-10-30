package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/events"
	"github.com/Fredrick-911/hotspot-platform/internal/payment"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// callbackLabel scopes the HMAC token that proves a callback came through the URL we gave PayHero.
const callbackLabel = "payhero"

type initiateReq struct {
	PlanID      string `json:"plan_id"`
	PhoneNumber string `json:"phone_number"`
	MacAddress  string `json:"mac_address"`
}

// handleInitiatePayment records a pending payment and triggers the M-Pesa STK push.
func (s *Server) handleInitiatePayment(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())

	var req initiateReq
	if !decode(w, r, &req) {
		return
	}
	phone, err := payment.NormalizeKenyanPhone(req.PhoneNumber)
	if err != nil {
		writeError(w, http.StatusBadRequest, "enter a valid Safaricom number, e.g. 0712 345 678")
		return
	}
	if !isUUID(req.PlanID) {
		writeError(w, http.StatusBadRequest, "choose a plan")
		return
	}

	// An open endpoint that sends M-Pesa prompts to any number can be used to
	// pester strangers, so each address and each number is limited.
	ipKey, phKey := "ip|"+s.clientIP(r), "ph|"+phone
	for _, k := range []string{ipKey, phKey} {
		if ok, wait := s.payLimiter.Allowed(k); !ok {
			writeError(w, http.StatusTooManyRequests, "too many payment attempts - try again in "+humanWait(wait))
			return
		}
	}
	s.payLimiter.Fail(ipKey)
	s.payLimiter.Fail(phKey)

	var (
		planName   string
		priceCents int
		active     bool
	)
	err = s.db.QueryRow(r.Context(),
		`SELECT name, price_cents, active FROM plans WHERE id = $1 AND tenant_id = $2`, req.PlanID, t.ID,
	).Scan(&planName, &priceCents, &active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		writeError(w, http.StatusBadRequest, "that plan is no longer available")
		return
	}
	if err != nil {
		serverError(w, "load plan", err)
		return
	}

	var mac *string
	if m, err := device.NormalizeMAC(req.MacAddress); err == nil {
		mac = &m
	}

	extRef := "WIFI-" + strings.ToUpper(uuid.New().String()[:12])
	var paymentID string
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO payments (tenant_id, plan_id, phone_number, amount_cents, external_reference, status, mac_address)
		VALUES ($1, $2, $3, $4, $5, 'PENDING', $6) RETURNING id::text`,
		t.ID, req.PlanID, phone, priceCents, extRef, mac).Scan(&paymentID)
	if err != nil {
		serverError(w, "create payment", err)
		return
	}

	resp := map[string]any{
		"payment_id": paymentID,
		"status":     "PENDING",
		"message":    "M-Pesa prompt sent. Check your phone and enter your PIN.",
	}
	switch {
	case s.cfg.PayHeroConfigured():
		cb := fmt.Sprintf("%s/api/payments/callback?t=%s", s.cfg.BaseURL, url.QueryEscape(s.signer.Token(callbackLabel, extRef)))
		client := payment.NewClient(s.cfg.PayHeroUsername, s.cfg.PayHeroPassword, s.cfg.PayHeroChannelID)
		if _, err := client.InitiateSTK(r.Context(), phone, priceCents/100, extRef, cb); err != nil {
			slog.Error("payhero STK push failed", "payment", paymentID, "err", err)
			_, _ = s.db.Exec(r.Context(),
				`UPDATE payments SET status = 'FAILED', result_desc = 'STK push could not be sent', updated_at = NOW() WHERE id = $1 AND status = 'PENDING'`, paymentID)
			writeError(w, http.StatusBadGateway, "we could not send the M-Pesa prompt - please try again")
			return
		}
	case s.cfg.DevPayments():
		slog.Warn("PayHero is not configured: payment waits for the dev 'simulate' button", "payment", paymentID)
		resp["dev_mode"] = true
		resp["message"] = "Development mode: no real M-Pesa prompt is sent."
	default:
		_, _ = s.db.Exec(r.Context(),
			`UPDATE payments SET status = 'FAILED', result_desc = 'payments not configured', updated_at = NOW() WHERE id = $1`, paymentID)
		writeError(w, http.StatusServiceUnavailable, "payments are not available right now")
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// payHeroCallback is the webhook body. PayHero sends the top-level "status" as
// a boolean in some versions and as text in others, so it is kept raw.
type payHeroCallback struct {
	Status   json.RawMessage `json:"status"`
	Response struct {
		Amount             float64 `json:"Amount"`
		Phone              string  `json:"Phone"`
		MpesaReceiptNumber string  `json:"MpesaReceiptNumber"`
		ExternalReference  string  `json:"ExternalReference"`
		ResultCode         int     `json:"ResultCode"`
		ResultDesc         string  `json:"ResultDesc"`
		Status             string  `json:"Status"`
	} `json:"response"`
}

// handlePaymentCallback receives PayHero's webhook. Only a caller that knows
// the secret token in the URL we registered for THIS payment can complete it.
func (s *Server) handlePaymentCallback(w http.ResponseWriter, r *http.Request) {
	var cb payHeroCallback
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&cb); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	ref := cb.Response.ExternalReference
	if ref == "" || !s.signer.CheckToken(callbackLabel, ref, r.URL.Query().Get("t")) {
		slog.Warn("payment callback rejected: bad token", "ref", ref, "ip", s.clientIP(r))
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var (
		id          string
		status      string
		amountCents int
	)
	err := s.db.QueryRow(r.Context(),
		`SELECT id::text, status, amount_cents FROM payments WHERE external_reference = $1`, ref).Scan(&id, &status, &amountCents)
	if err != nil || status != "PENDING" {
		w.WriteHeader(http.StatusOK) // unknown or already settled: acknowledge so PayHero stops retrying
		return
	}

	resp := cb.Response
	switch {
	case resp.ResultCode == 0 && resp.MpesaReceiptNumber != "" && !failedWord(resp.Status):
		if resp.Amount > 0 && int(resp.Amount*100+0.5) < amountCents {
			slog.Error("payment callback: amount lower than the price", "ref", ref, "paid", resp.Amount, "price_cents", amountCents)
			s.failPayment(r.Context(), id, "amount paid was lower than the price")
			break
		}
		if _, err := s.completePayment(r.Context(), id, resp.MpesaReceiptNumber, resp.ResultDesc); err != nil {
			// Not acknowledged as done: PayHero retries, and the status poller also finishes the job.
			slog.Error("payment callback: provisioning failed", "ref", ref, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	case resp.ResultCode != 0 || failedWord(resp.Status):
		s.failPayment(r.Context(), id, resp.ResultDesc)
	default:
		slog.Warn("payment callback without a receipt left pending", "ref", ref)
	}
	w.WriteHeader(http.StatusOK)
}

func failedWord(st string) bool {
	switch strings.ToLower(st) {
	case "failed", "cancelled", "canceled":
		return true
	}
	return false
}

func (s *Server) failPayment(ctx context.Context, id, desc string) {
	if len(desc) > 200 {
		desc = desc[:200]
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE payments SET status = 'FAILED', result_desc = $2, updated_at = NOW() WHERE id = $1 AND status = 'PENDING'`, id, desc); err != nil {
		slog.Error("mark payment failed", "payment", id, "err", err)
	}
}

// completePayment is the ONE place a payment becomes a pass. It may be called
// by the webhook, the status poller and the dev button, in any order and more
// than once: the status flips at most once, the pass is created at most once,
// and the owner's screen gets one "sale" event.
func (s *Server) completePayment(ctx context.Context, paymentID, receipt, desc string) (*session.Session, error) {
	var rcpt *string
	if receipt != "" {
		rcpt = &receipt
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE payments p
		SET status = 'SUCCESS', mpesa_receipt = $2, result_desc = $3,
		    fee_cents = ((p.amount_cents::bigint * t.commission_bps) / 10000)::int, updated_at = NOW()
		FROM tenants t
		WHERE p.id = $1 AND p.status = 'PENDING' AND t.id = p.tenant_id`, paymentID, rcpt, desc)
	if err != nil {
		return nil, fmt.Errorf("mark payment paid: %w", err)
	}
	flipped := tag.RowsAffected() > 0

	sess, planName, amount, err := s.provisionPaid(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	if flipped {
		s.hub.Publish("sale", map[string]any{
			"payment_id": paymentID, "plan_name": planName, "amount_cents": amount, "phone": maskPhone(sess.PhoneNumber),
		}, events.TenantTopic(sess.TenantID), events.AdminTopic)
	}
	return sess, nil
}

// provisionPaid creates (or returns) the pass for a payment that is SUCCESS.
func (s *Server) provisionPaid(ctx context.Context, paymentID string) (*session.Session, string, int, error) {
	var (
		tenantID, planID, phone, status, planName string
		mac                                       *string
		amount, minutes                           int
		speed                                     *string
		dataLimit                                 *int64
	)
	err := s.db.QueryRow(ctx, `
		SELECT p.tenant_id::text, p.plan_id::text, p.phone_number, p.mac_address, p.amount_cents, p.status,
		       pl.name, pl.duration_minutes, pl.speed_limit, pl.data_limit_bytes
		FROM payments p JOIN plans pl ON pl.id = p.plan_id WHERE p.id = $1`, paymentID,
	).Scan(&tenantID, &planID, &phone, &mac, &amount, &status, &planName, &minutes, &speed, &dataLimit)
	if err != nil {
		return nil, "", 0, fmt.Errorf("load paid payment: %w", err)
	}
	if status != "SUCCESS" {
		return nil, "", 0, fmt.Errorf("payment %s is %s, not SUCCESS", paymentID, status)
	}
	sess, err := session.Provision(ctx, s.db, session.Params{
		TenantID: tenantID, PlanID: planID, PaymentID: paymentID, Phone: phone, MAC: mac,
		DurationMinutes: minutes, SpeedLimit: speed, DataLimitBytes: dataLimit,
	})
	if err != nil {
		return nil, "", 0, err
	}
	return sess, planName, amount, nil
}

func maskPhone(p string) string {
	if len(p) < 8 {
		return p
	}
	return p[:4] + "***" + p[len(p)-3:]
}

// shouldAskPayHero throttles the active look-up so a polling phone cannot
// hammer PayHero's API: at most one check per payment every 8 seconds.
func (s *Server) shouldAskPayHero(id string) bool {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	now := time.Now()
	if last, ok := s.lastChecked[id]; ok && now.Sub(last) < 8*time.Second {
		return false
	}
	if len(s.lastChecked) > 5000 {
		for k, v := range s.lastChecked {
			if now.Sub(v) > time.Minute {
				delete(s.lastChecked, k)
			}
		}
	}
	s.lastChecked[id] = now
	return true
}

// handlePaymentStatus is polled by the portal while the customer enters their M-Pesa PIN.
func (s *Server) handlePaymentStatus(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	id := r.URL.Query().Get("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}

	var (
		status, extRef string
		receipt, desc  *string
	)
	err := s.db.QueryRow(r.Context(), `
		SELECT status, external_reference, mpesa_receipt, result_desc FROM payments WHERE id = $1 AND tenant_id = $2`,
		id, t.ID).Scan(&status, &extRef, &receipt, &desc)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	if err != nil {
		serverError(w, "payment status", err)
		return
	}

	// Safety net: if the webhook never arrived, ask PayHero directly.
	if status == "PENDING" && s.cfg.PayHeroConfigured() && s.shouldAskPayHero(id) {
		client := payment.NewClient(s.cfg.PayHeroUsername, s.cfg.PayHeroPassword, s.cfg.PayHeroChannelID)
		if tx, err := client.FindInboundPayment(r.Context(), extRef); err == nil && tx != nil {
			if _, err := s.completePayment(r.Context(), id, tx.ProviderReference, "Confirmed via PayHero API"); err != nil {
				slog.Error("status poll: could not complete payment", "payment", id, "err", err)
			} else {
				status, receipt = "SUCCESS", &tx.ProviderReference
			}
		}
	}

	resp := map[string]any{"status": status}
	switch status {
	case "SUCCESS":
		sess, _, _, err := s.provisionPaid(r.Context(), id)
		if err != nil {
			serverError(w, "provision on status", err)
			return
		}
		if receipt != nil {
			resp["receipt"] = *receipt
		}
		resp["username"], resp["pin"], resp["expires_at"] = sess.Username, sess.Password, sess.ExpiresAt
		s.giveCustomerCookie(w, sess)
	case "FAILED":
		if desc != nil {
			resp["message"] = *desc
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDevConfirm lets you finish a payment without M-Pesa while developing.
// It does not exist unless APP_ENV=development AND PayHero is not configured.
func (s *Server) handleDevConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DevPayments() {
		http.NotFound(w, r)
		return
	}
	t := tenant.FromContext(r.Context())
	var req struct {
		PaymentID string `json:"payment_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !isUUID(req.PaymentID) {
		writeError(w, http.StatusBadRequest, "bad payment id")
		return
	}
	var one int
	if err := s.db.QueryRow(r.Context(), `SELECT 1 FROM payments WHERE id = $1 AND tenant_id = $2`, req.PaymentID, t.ID).Scan(&one); err != nil {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	if _, err := s.completePayment(r.Context(), req.PaymentID, "DEV"+strings.ToUpper(hex.EncodeToString(b)), "Simulated payment"); err != nil {
		serverError(w, "dev confirm", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "SUCCESS"})
}

// giveCustomerCookie lets the customer open "My account" without logging in again.
func (s *Server) giveCustomerCookie(w http.ResponseWriter, sess *session.Session) {
	tok, err := s.signer.Sign(auth.Claims{Kind: "customer", Subject: sess.ID, TenantID: sess.TenantID}, customerTTL)
	if err != nil {
		return
	}
	setCookie(w, cookieCustomer, tok, customerTTL, s.cfg.CookieSecure())
}
