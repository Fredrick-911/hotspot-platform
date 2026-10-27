package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Fredrick-911/hotspot-platform/internal/config"
	"github.com/Fredrick-911/hotspot-platform/internal/payment"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type initiatePaymentReq struct {
	PlanID      string  `json:"plan_id"`
	PhoneNumber string  `json:"phone_number"`
	MacAddress  *string `json:"mac_address,omitempty"`
}

// handleInitiatePayment triggers PayHero M-Pesa STK push.
func handleInitiatePayment(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, cfg config.Config) {
	t := tenant.FromContext(r.Context())

	var req initiatePaymentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request payload"}`, http.StatusBadRequest)
		return
	}

	normPhone, err := payment.NormalizeKenyanPhone(req.PhoneNumber)
	if err != nil {
		http.Error(w, `{"error":"invalid Kenyan phone number"}`, http.StatusBadRequest)
		return
	}

	// Look up the chosen plan
	var planName string
	var priceCents, durationMinutes int
	var speedLimit *string
	var dataLimitBytes *int64
	err = db.QueryRow(r.Context(), `
		SELECT name, price_cents, duration_minutes, speed_limit, data_limit_bytes 
		FROM plans WHERE id = $1 AND tenant_id = $2
	`, req.PlanID, t.ID).Scan(&planName, &priceCents, &durationMinutes, &speedLimit, &dataLimitBytes)
	if err != nil {
		http.Error(w, `{"error":"plan not found"}`, http.StatusBadRequest)
		return
	}

	externalRef := fmt.Sprintf("WIFI-%s", strings.ToUpper(uuid.New().String()[:12]))
	amountShillings := priceCents / 100

	// 1. Record pending payment in database
	var paymentID string
	err = db.QueryRow(r.Context(), `
		INSERT INTO payments (tenant_id, plan_id, phone_number, amount_cents, external_reference, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
		RETURNING id::text
	`, t.ID, req.PlanID, normPhone, priceCents, externalRef).Scan(&paymentID)
	if err != nil {
		slog.Error("failed to create payment record", "err", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	// 2. Call PayHero API (if configured)
	if cfg.PayHeroUsername != "" && cfg.PayHeroChannelID > 0 {
		client := payment.NewClient(cfg.PayHeroUsername, cfg.PayHeroPassword, cfg.PayHeroChannelID)
		callbackURL := fmt.Sprintf("%s/api/payments/callback", cfg.BaseURL)

		_, err = client.InitiateSTK(r.Context(), normPhone, amountShillings, externalRef, callbackURL)
		if err != nil {
			slog.Error("payhero STK push failed", "phone", normPhone, "err", err)
			http.Error(w, `{"error":"failed to initiate M-Pesa prompt"}`, http.StatusBadGateway)
			return
		}
	} else {
		slog.Warn("PayHero not configured in .env, payment created in dev-mock mode", "payment_id", paymentID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"payment_id":         paymentID,
		"external_reference": externalRef,
		"status":             "PENDING",
		"message":            "M-Pesa STK push sent. Please check your phone.",
	})
}

// PayHeroCallbackPayload represents the webhook sent by PayHero upon payment completion.
type payHeroCallbackPayload struct {
	Status   string `json:"status"` // "Success" or "Failed"
	Response struct {
		Amount             int    `json:"Amount"`
		Phone              string `json:"Phone"`
		MpesaReceiptNumber string `json:"MpesaReceiptNumber"`
		ExternalReference  string `json:"ExternalReference"`
		ResultCode         int    `json:"ResultCode"`
		ResultDesc         string `json:"ResultDesc"`
	} `json:"response"`
}

// handlePaymentCallback processes the asynchronous webhook from PayHero.
func handlePaymentCallback(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var payload payHeroCallbackPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	extRef := payload.Response.ExternalReference
	if extRef == "" {
		http.Error(w, "missing external_reference", http.StatusBadRequest)
		return
	}

	// Find the matching payment
	var paymentID, tenantID, planID, phone string
	var durationMinutes int
	var speedLimit *string
	var dataLimitBytes *int64
	err := db.QueryRow(r.Context(), `
		SELECT p.id::text, p.tenant_id::text, p.plan_id::text, p.phone_number, pl.duration_minutes, pl.speed_limit, pl.data_limit_bytes
		FROM payments p
		JOIN plans pl ON pl.id = p.plan_id
		WHERE p.external_reference = $1 AND p.status = 'PENDING'
	`, extRef).Scan(&paymentID, &tenantID, &planID, &phone, &durationMinutes, &speedLimit, &dataLimitBytes)
	if err != nil {
		slog.Warn("callback received for unknown or resolved payment", "ref", extRef, "err", err)
		w.WriteHeader(http.StatusOK) // Acknowledge to PayHero so it stops retrying
		return
	}

	if payload.Response.ResultCode == 0 || strings.EqualFold(payload.Status, "Success") {
		// Update payment to SUCCESS
		_, _ = db.Exec(r.Context(), `
			UPDATE payments 
			SET status = 'SUCCESS', mpesa_receipt = $1, result_desc = $2, updated_at = NOW()
			WHERE id = $3
		`, payload.Response.MpesaReceiptNumber, payload.Response.ResultDesc, paymentID)

		// Provision FreeRADIUS session
		sess, err := session.ProvisionUser(r.Context(), db, tenantID, planID, paymentID, phone, nil, durationMinutes, speedLimit, dataLimitBytes)
		if err != nil {
			slog.Error("failed to provision user in FreeRADIUS after payment", "err", err)
		} else {
			slog.Info("user successfully provisioned in FreeRADIUS", "username", sess.Username, "pin", sess.Password)
		}
	} else {
		// Mark payment FAILED
		_, _ = db.Exec(r.Context(), `
			UPDATE payments 
			SET status = 'FAILED', result_desc = $1, updated_at = NOW()
			WHERE id = $2
		`, payload.Response.ResultDesc, paymentID)
	}

	w.WriteHeader(http.StatusOK)
}

func handleCheckPaymentStatus(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, cfg config.Config, paymentID string) {
	var status string
	var receipt *string
	var extRef, tenantID, planID, phone string
	var durationMinutes int
	var speedLimit *string
	var dataLimitBytes *int64
	var pin, username string

	err := db.QueryRow(r.Context(), `
		SELECT p.status, p.mpesa_receipt, p.external_reference, p.tenant_id::text, p.plan_id::text, p.phone_number,
		       pl.duration_minutes, pl.speed_limit, pl.data_limit_bytes
		FROM payments p
		JOIN plans pl ON pl.id = p.plan_id
		WHERE p.id = $1
	`, paymentID).Scan(&status, &receipt, &extRef, &tenantID, &planID, &phone, &durationMinutes, &speedLimit, &dataLimitBytes)
	if err != nil {
		http.Error(w, `{"error":"payment not found"}`, http.StatusNotFound)
		return
	}

	// Active Fallback: If still PENDING, verify directly with PayHero API
	if status == "PENDING" && cfg.PayHeroUsername != "" && cfg.PayHeroChannelID > 0 {
		client := payment.NewClient(cfg.PayHeroUsername, cfg.PayHeroPassword, cfg.PayHeroChannelID)
		tx, err := client.FindInboundPayment(r.Context(), extRef)
		if err == nil && tx != nil {
			cmdTag, err := db.Exec(r.Context(), `
				UPDATE payments 
				SET status = 'SUCCESS', mpesa_receipt = $1, result_desc = 'Reconciled via PayHero API', updated_at = NOW()
				WHERE id = $2 AND status = 'PENDING'
			`, tx.ProviderReference, paymentID)

			if err == nil && cmdTag.RowsAffected() > 0 {
				status = "SUCCESS"
				receipt = &tx.ProviderReference
				sess, err := session.ProvisionUser(r.Context(), db, tenantID, planID, paymentID, phone, nil, durationMinutes, speedLimit, dataLimitBytes)
				if err != nil {
					slog.Error("failed to provision user after PayHero active check", "err", err)
				} else {
					slog.Info("active reconciliation provisioned user in FreeRADIUS", "username", sess.Username, "pin", sess.Password)
					username = sess.Username
					pin = sess.Password
				}
			}
		}
	}

	resp := map[string]any{"status": status}
	if receipt != nil {
		resp["receipt"] = *receipt
	}

	// If successful and pin wasn't populated from active check, load from DB
	if status == "SUCCESS" {
		if pin == "" {
			_ = db.QueryRow(r.Context(), `SELECT username, password FROM sessions WHERE payment_id = $1`, paymentID).Scan(&username, &pin)
		}
		resp["username"] = username
		resp["pin"] = pin
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type reconnectReq struct {
	PhoneOrPIN string  `json:"phone_or_pin"`
	MacAddress *string `json:"mac_address,omitempty"`
}

// handleReconnectSession allows users with active sessions to reconnect without paying again.
func handleReconnectSession(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	var req reconnectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request payload"}`, http.StatusBadRequest)
		return
	}

	sess, err := session.ReconnectByPhoneOrPIN(r.Context(), db, t.ID, strings.TrimSpace(req.PhoneOrPIN), req.MacAddress)
	if err != nil {
		http.Error(w, `{"error":"No active pass found for this phone or PIN"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ACTIVE",
		"username":   sess.Username,
		"pin":        sess.Password,
		"expires_at": sess.ExpiresAt,
	})
}
