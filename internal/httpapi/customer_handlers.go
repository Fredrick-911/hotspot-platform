package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/events"
	"github.com/Fredrick-911/hotspot-platform/internal/payment"
	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// planView is one plan card on the portal.
type planView struct {
	ID       string
	Name     string
	PriceKES int
	Duration string
	Data     string
	Speed    string // "" when uncapped
	Accent   int    // 0..3: cycles the card colour like the Campus Wi-Fi design
}

type portalData struct {
	Tenant        *tenant.Tenant
	Plans         []planView
	Announcements []dashboard.Announcement
	DevPayments   bool
	TenantQ       string // "?tenant=x" when the portal was opened that way
}

func speedText(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	up, down, ok := strings.Cut(*s, "/")
	if !ok {
		return *s
	}
	return up + " up / " + down + " down"
}

func (s *Server) handlePortal(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	plans, err := plan.ListByTenant(r.Context(), s.db, t.ID)
	if err != nil {
		serverError(w, "portal plans", err)
		return
	}
	notes, err := s.store.Announcements(r.Context(), t.ID, true)
	if err != nil {
		serverError(w, "portal announcements", err)
		return
	}
	views := make([]planView, len(plans))
	for i, p := range plans {
		views[i] = planView{
			ID: p.ID, Name: p.Name, PriceKES: p.PriceCents / 100, Duration: p.FormattedDuration(),
			Data: p.FormattedDataLimit(), Speed: speedText(p.SpeedLimit), Accent: i % 4,
		}
	}
	s.render(w, "portal.html", portalData{
		Tenant: t, Plans: views, Announcements: notes,
		DevPayments: s.cfg.DevPayments(), TenantQ: tenantQuery(r, s.cfg.AllowTenantQuery),
	})
}

func (s *Server) handleAccountPage(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	s.render(w, "account.html", portalData{
		Tenant: t, TenantQ: tenantQuery(r, s.cfg.AllowTenantQuery),
	})
}

func (s *Server) handlePortalAnnouncements(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	notes, err := s.store.Announcements(r.Context(), t.ID, true)
	if err != nil {
		serverError(w, "portal announcements", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcements": notes})
}

func (s *Server) handlePortalStream(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, events.PortalTopic(tenant.FromContext(r.Context()).ID))
}

type reconnectReq struct {
	Phone      string `json:"phone"`
	PIN        string `json:"pin"`
	MacAddress string `json:"mac_address"`
}

// guessLimited applies the shared limit on phone+PIN guessing. A PIN is only
// 4 digits, so without this anyone could try all 9,000 for a known phone number.
func (s *Server) guessLimited(w http.ResponseWriter, r *http.Request, phone string) ([]string, bool) {
	keys := []string{"ip|" + s.clientIP(r), "ph|" + phone}
	for _, k := range keys {
		if ok, wait := s.custLimiter.Allowed(k); !ok {
			writeError(w, http.StatusTooManyRequests, "too many wrong attempts - try again in "+humanWait(wait))
			return nil, false
		}
	}
	return keys, true
}

// handleReconnect restores a live pass when the customer gives BOTH the phone
// they paid with and the PIN they were shown.
func (s *Server) handleReconnect(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	var req reconnectReq
	if !decode(w, r, &req) {
		return
	}
	phone, err := payment.NormalizeKenyanPhone(req.Phone)
	pin := strings.TrimSpace(req.PIN)
	if err != nil || len(pin) != 4 {
		writeError(w, http.StatusBadRequest, "enter the phone number you paid with and your 4-digit PIN")
		return
	}
	keys, ok := s.guessLimited(w, r, phone)
	if !ok {
		return
	}
	var mac *string
	if m, err := device.NormalizeMAC(req.MacAddress); err == nil {
		mac = &m
	}

	sess, err := session.Reconnect(r.Context(), s.db, t.ID, phone, pin, mac)
	switch {
	case errors.Is(err, session.ErrNoActiveSession):
		for _, k := range keys {
			s.custLimiter.Fail(k)
		}
		writeError(w, http.StatusNotFound, "no active pass found for that phone number and PIN")
		return
	case errors.Is(err, session.ErrDataExhausted):
		writeError(w, http.StatusForbidden, "this pass has used all of its data - please buy a new one")
		return
	case err != nil:
		serverError(w, "reconnect", err)
		return
	}
	for _, k := range keys {
		s.custLimiter.Reset(k)
	}
	s.giveCustomerCookie(w, sess)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ACTIVE", "username": sess.Username, "pin": sess.Password, "expires_at": sess.ExpiresAt,
	})
}

// handleCustomerLogin opens "My account" with phone + PIN. Unlike reconnect it
// also works for passes that have ended (receipts stay visible).
func (s *Server) handleCustomerLogin(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	var req reconnectReq
	if !decode(w, r, &req) {
		return
	}
	phone, err := payment.NormalizeKenyanPhone(req.Phone)
	pin := strings.TrimSpace(req.PIN)
	if err != nil || len(pin) != 4 {
		writeError(w, http.StatusBadRequest, "enter the phone number you paid with and your 4-digit PIN")
		return
	}
	keys, ok := s.guessLimited(w, r, phone)
	if !ok {
		return
	}
	var sess session.Session
	err = s.db.QueryRow(r.Context(), `
		SELECT id::text, tenant_id::text FROM sessions
		WHERE tenant_id = $1 AND phone_number = $2 AND password = $3
		ORDER BY starts_at DESC LIMIT 1`, t.ID, phone, pin).Scan(&sess.ID, &sess.TenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		for _, k := range keys {
			s.custLimiter.Fail(k)
		}
		writeError(w, http.StatusUnauthorized, "that phone number and PIN do not match a pass")
		return
	}
	if err != nil {
		serverError(w, "customer login", err)
		return
	}
	for _, k := range keys {
		s.custLimiter.Reset(k)
	}
	s.giveCustomerCookie(w, &sess)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCustomerLogout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, cookieCustomer, s.cfg.CookieSecure())
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) handleCustomerMe(w http.ResponseWriter, r *http.Request) {
	t := tenant.FromContext(r.Context())
	c, err := r.Cookie(cookieCustomer)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "sign in to see your pass")
		return
	}
	claims, err := s.signer.Verify(c.Value)
	// The cookie must belong to the tenant in the address bar: with ?tenant= two
	// businesses share a host name, and one's cookie must never open the other's pass.
	if err != nil || claims.Kind != "customer" || claims.TenantID != t.ID {
		writeError(w, http.StatusUnauthorized, "sign in to see your pass")
		return
	}
	acc, err := s.store.Account(r.Context(), t.ID, claims.Subject)
	if errors.Is(err, dashboard.ErrAccountNotFound) {
		writeError(w, http.StatusUnauthorized, "sign in to see your pass")
		return
	}
	if err != nil {
		serverError(w, "customer account", err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}
