package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
)

// staffHandler is a handler that already knows who is logged in.
type staffHandler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// staffFromRequest returns the logged-in staff user, or nil.
func (s *Server) staffFromRequest(r *http.Request) *auth.User {
	if s.db == nil {
		return nil
	}
	c, err := r.Cookie(cookieStaff)
	if err != nil {
		return nil
	}
	claims, err := s.signer.Verify(c.Value)
	if err != nil || claims.Kind != "staff" {
		return nil
	}
	u, err := auth.ByID(r.Context(), s.db, claims.Subject)
	if err != nil || !u.Active {
		return nil
	}
	return u
}

// requireStaff protects an API route. role "" accepts any staff user.
func (s *Server) requireStaff(role string, h staffHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.staffFromRequest(r)
		if u == nil {
			writeError(w, http.StatusUnauthorized, "please sign in")
			return
		}
		if role != "" && u.Role != role {
			writeError(w, http.StatusForbidden, "you do not have access to this")
			return
		}
		h(w, r, u)
	}
}

// requirePage protects a dashboard page: strangers go to /login, and a user
// who opens the wrong dashboard is sent to their own.
func (s *Server) requirePage(role string, h staffHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.staffFromRequest(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.Role != role {
			http.Redirect(w, r, homeFor(u.Role), http.StatusSeeOther)
			return
		}
		h(w, r, u)
	}
}

func homeFor(role string) string {
	if role == auth.RoleAdmin {
		return "/admin"
	}
	return "/dashboard"
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if u := s.staffFromRequest(r); u != nil {
		http.Redirect(w, r, homeFor(u.Role), http.StatusSeeOther)
		return
	}
	s.render(w, "login.html", nil)
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	ip := s.clientIP(r)
	keys := []*struct {
		l *auth.Limiter
		k string
	}{{s.loginLimiter, ip + "|" + email}, {s.loginIPLimiter, ip}}
	for _, k := range keys {
		if ok, wait := k.l.Allowed(k.k); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "too many attempts - try again in "+humanWait(wait))
			return
		}
	}

	u, err := auth.Authenticate(r.Context(), s.db, email, req.Password)
	if errors.Is(err, auth.ErrInvalidLogin) {
		for _, k := range keys {
			k.l.Fail(k.k)
		}
		writeError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if err != nil {
		serverError(w, "login", err)
		return
	}
	s.loginLimiter.Reset(ip + "|" + email)

	tok, err := s.signer.Sign(auth.Claims{Kind: "staff", Subject: u.ID}, staffTTL)
	if err != nil {
		serverError(w, "sign session", err)
		return
	}
	setCookie(w, cookieStaff, tok, staffTTL, s.cfg.CookieSecure())
	writeJSON(w, http.StatusOK, map[string]any{"role": u.Role, "redirect": homeFor(u.Role)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, cookieStaff, s.cfg.CookieSecure())
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *auth.User) {
	writeJSON(w, http.StatusOK, u)
}

type passwordReq struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

const minPasswordLen = 10

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req passwordReq
	if !decode(w, r, &req) {
		return
	}
	if len(req.New) < minPasswordLen || len(req.New) > 200 {
		writeError(w, http.StatusBadRequest, "new password must be at least 10 characters")
		return
	}
	key := "pw|" + u.ID
	if ok, wait := s.loginLimiter.Allowed(key); !ok {
		writeError(w, http.StatusTooManyRequests, "too many attempts - try again in "+humanWait(wait))
		return
	}
	if _, err := auth.Authenticate(r.Context(), s.db, u.Email, req.Current); err != nil {
		s.loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "your current password is wrong")
		return
	}
	if err := auth.SetPassword(r.Context(), s.db, u.ID, req.New); err != nil {
		serverError(w, "set password", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}

// ---- dashboard pages ----

type pageData struct {
	User      *auth.User
	Tenant    *tenant.Tenant
	PortalURL string
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	s.render(w, "admin.html", pageData{User: u})
}

func (s *Server) handleTenantPage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	t, err := tenant.ByID(r.Context(), s.db, *u.TenantID)
	if err != nil {
		serverError(w, "load tenant", err)
		return
	}
	s.render(w, "tenant.html", pageData{User: u, Tenant: t, PortalURL: s.portalURL(t)})
}

// portalURL is the address customers use: the tenant's own subdomain once DNS
// exists, otherwise the ?tenant= form on the main address.
func (s *Server) portalURL(t *tenant.Tenant) string {
	return s.cfg.BaseURL + "/?tenant=" + t.Subdomain
}

func humanWait(d time.Duration) string {
	if d < time.Minute {
		return "a minute"
	}
	return strconv.Itoa(int(d.Minutes())+1) + " minutes"
}

func hostOnly(h string) string {
	if i := strings.LastIndex(h, ":"); i != -1 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}
