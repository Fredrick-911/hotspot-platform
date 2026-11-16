// internal/httpapi/billing_handlers.go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/billing"
	"github.com/jackc/pgx/v5"
)

type subSTKReq struct {
	Phone string `json:"phone"`
}

// tenantSubscriptionSTK triggers an IntaSend M-Pesa STK push for the box owner's subscription.
func (s *Server) tenantSubscriptionSTK(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if !s.cfg.IntaSendConfigured() {
		writeError(w, http.StatusServiceUnavailable, "IntaSend billing is not configured on this server")
		return
	}
	if u.TenantID == nil || *u.TenantID == "" {
		writeError(w, http.StatusBadRequest, "no tenant associated with user")
		return
	}
	tenantID := *u.TenantID

	var in subSTKReq
	if !decode(w, r, &in) {
		return
	}

	ctx := r.Context()
	var amountKES int
	var defaultPhone *string
	err := s.db.QueryRow(ctx, `SELECT subscription_amount_kes, contact_phone FROM tenants WHERE id = $1`, tenantID).Scan(&amountKES, &defaultPhone)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		serverError(w, "load tenant billing details", err)
		return
	}

	phoneToUse := strings.TrimSpace(in.Phone)
	if phoneToUse == "" && defaultPhone != nil {
		phoneToUse = *defaultPhone
	}
	if phoneToUse == "" {
		writeError(w, http.StatusBadRequest, "phone number is required for M-Pesa STK push")
		return
	}

	client := billing.NewClient(s.cfg.IntaSendPublishableKey, s.cfg.IntaSendSecretKey)
	apiRef := fmt.Sprintf("sub_%s_%d", tenantID[:8], time.Now().Unix())

	resp, err := client.SendSTKPush(ctx, amountKES, phoneToUse, apiRef)
	if err != nil {
		slog.Error("intasend STK push failed", "tenant", tenantID, "err", err)
		writeError(w, http.StatusBadGateway, "failed to trigger M-Pesa prompt: "+err.Error())
		return
	}

	// Record pending invoice
	_, err = s.db.Exec(ctx, `
		INSERT INTO platform_invoices (tenant_id, invoice_id, amount_kes, account, status)
		VALUES ($1, $2, $3, $4, 'PENDING')
		ON CONFLICT (invoice_id) DO NOTHING`,
		tenantID, resp.Invoice.InvoiceID, amountKES, phoneToUse)
	if err != nil {
		slog.Error("failed to record pending invoice", "invoice_id", resp.Invoice.InvoiceID, "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"invoice_id": resp.Invoice.InvoiceID,
		"state":      resp.Invoice.State,
		"amount_kes": amountKES,
		"message":    "M-Pesa payment prompt sent to your phone",
	})
}

// tenantSubscriptionCheckout generates an IntaSend hosted checkout URL (for Card/Apple Pay/Bank).
func (s *Server) tenantSubscriptionCheckout(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if !s.cfg.IntaSendConfigured() {
		writeError(w, http.StatusServiceUnavailable, "IntaSend billing is not configured on this server")
		return
	}
	if u.TenantID == nil || *u.TenantID == "" {
		writeError(w, http.StatusBadRequest, "no tenant associated with user")
		return
	}
	tenantID := *u.TenantID

	ctx := r.Context()
	var amountKES int
	var tName string
	err := s.db.QueryRow(ctx, `SELECT name, subscription_amount_kes FROM tenants WHERE id = $1`, tenantID).Scan(&tName, &amountKES)
	if err != nil {
		serverError(w, "load tenant for checkout", err)
		return
	}

	client := billing.NewClient(s.cfg.IntaSendPublishableKey, s.cfg.IntaSendSecretKey)
	apiRef := fmt.Sprintf("sub_%s_%d", tenantID[:8], time.Now().Unix())
	redirectURL := s.cfg.BaseURL + "/dashboard?billing=complete"

	resp, err := client.CreateCheckout(ctx, billing.CheckoutRequest{
		Amount:      float64(amountKES),
		Currency:    "KES",
		Email:       u.Email,
		FirstName:   tName,
		APIRef:      apiRef,
		RedirectURL: redirectURL,
	})
	if err != nil {
		slog.Error("intasend create checkout failed", "tenant", tenantID, "err", err)
		writeError(w, http.StatusBadGateway, "failed to create payment session")
		return
	}

	_, _ = s.db.Exec(ctx, `
		INSERT INTO platform_invoices (tenant_id, invoice_id, amount_kes, account, status)
		VALUES ($1, $2, $3, $4, 'PENDING')
		ON CONFLICT (invoice_id) DO NOTHING`,
		tenantID, resp.ID, amountKES, u.Email)

	writeJSON(w, http.StatusOK, map[string]any{
		"checkout_url": resp.URL,
		"id":           resp.ID,
		"amount_kes":   amountKES,
	})
}

// tenantSubscriptionStatus queries IntaSend for invoice status and activates the subscription if paid.
func (s *Server) tenantSubscriptionStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if !s.cfg.IntaSendConfigured() {
		writeError(w, http.StatusServiceUnavailable, "IntaSend billing is not configured on this server")
		return
	}
	invoiceID := strings.TrimSpace(r.URL.Query().Get("invoice_id"))
	if invoiceID == "" {
		writeError(w, http.StatusBadRequest, "invoice_id query parameter is required")
		return
	}

	ctx := r.Context()
	client := billing.NewClient(s.cfg.IntaSendPublishableKey, s.cfg.IntaSendSecretKey)
	res, err := client.CheckPaymentStatus(ctx, invoiceID)
	if err != nil {
		slog.Error("failed to check intasend payment status", "invoice_id", invoiceID, "err", err)
		writeError(w, http.StatusBadGateway, "could not verify invoice status")
		return
	}

	state := res.Invoice.State
	if state == "COMPLETE" {
		ref := ""
		if res.Invoice.ProviderRef != nil {
			ref = *res.Invoice.ProviderRef
		}
		if err := s.activateSubscription(ctx, invoiceID, ref); err != nil {
			slog.Error("failed to activate tenant subscription", "invoice_id", invoiceID, "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"state":   "COMPLETE",
			"paid":    true,
			"receipt": ref,
			"message": "Subscription renewed successfully",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state": state,
		"paid":  false,
	})
}

// handleIntaSendWebhook processes collection_event webhooks directly from IntaSend.
func (s *Server) handleIntaSendWebhook(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		InvoiceID   string `json:"invoice_id"`
		State       string `json:"state"`
		ProviderRef string `json:"provider_ref"`
		Invoice     struct {
			InvoiceID   string `json:"invoice_id"`
			State       string `json:"state"`
			ProviderRef string `json:"provider_ref"`
		} `json:"invoice"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	invID := payload.InvoiceID
	if invID == "" {
		invID = payload.Invoice.InvoiceID
	}
	state := payload.State
	if state == "" {
		state = payload.Invoice.State
	}
	ref := payload.ProviderRef
	if ref == "" {
		ref = payload.Invoice.ProviderRef
	}

	if invID != "" && state == "COMPLETE" {
		if err := s.activateSubscription(r.Context(), invID, ref); err != nil {
			slog.Error("failed to activate subscription from webhook", "invoice_id", invID, "err", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) activateSubscription(ctx context.Context, invoiceID, providerRef string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID string
	err = tx.QueryRow(ctx, `
		UPDATE platform_invoices
		SET status = 'COMPLETE', provider_ref = $1, updated_at = NOW()
		WHERE invoice_id = $2
		RETURNING tenant_id::text`, providerRef, invoiceID).Scan(&tenantID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	if tenantID != "" {
		// Extend subscription by 30 days and mark active
		_, err = tx.Exec(ctx, `
			UPDATE tenants
			SET status = 'ACTIVE',
			    subscription_expires_at = COALESCE(GREATEST(subscription_expires_at, NOW()), NOW()) + INTERVAL '30 days'
			WHERE id = $1`, tenantID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
