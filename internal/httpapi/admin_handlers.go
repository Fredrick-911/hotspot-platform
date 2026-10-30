package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/events"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
)

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	o, err := s.store.AdminOverview(r.Context())
	if err != nil {
		serverError(w, "admin overview", err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) adminTenants(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	rows, err := s.store.Tenants(r.Context())
	if err != nil {
		serverError(w, "admin tenants", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": rows})
}

// tenantInputError turns a validation error from the tenant/auth packages into a 400.
func tenantInputError(err error) (string, bool) {
	for _, known := range []error{
		tenant.ErrSubdomainTaken, tenant.ErrBadSubdomain, tenant.ErrReservedName, tenant.ErrBadCommission,
		tenant.ErrBadTenantName, tenant.ErrTenantNotFound, auth.ErrEmailTaken, auth.ErrBadEmail, auth.ErrUserNotFound,
	} {
		if errors.Is(err, known) {
			return known.Error(), true
		}
	}
	return "", false
}

func (s *Server) adminCreateTenant(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var in dashboard.NewTenant
	if !decode(w, r, &in) {
		return
	}
	created, err := s.store.CreateTenant(r.Context(), in)
	if msg, ok := tenantInputError(err); ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		serverError(w, "create tenant", err)
		return
	}
	s.hub.Publish("tenant", map[string]string{"name": created.Tenant.Name}, events.AdminTopic)
	writeJSON(w, http.StatusCreated, created)
}

type updateTenantReq struct {
	Status        *string `json:"status"`
	CommissionBPS *int    `json:"commission_bps"`
}

func (s *Server) adminUpdateTenant(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in updateTenantReq
	if !decode(w, r, &in) {
		return
	}
	err := s.store.UpdateTenant(r.Context(), id, in.Status, in.CommissionBPS)
	if msg, ok := tenantInputError(err); ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		if in.Status != nil && *in.Status != tenant.StatusActive && *in.Status != tenant.StatusSuspended {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		serverError(w, "update tenant", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	email, pw, err := s.store.ResetOwnerPassword(r.Context(), id)
	if errors.Is(err, auth.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "this business has no owner login")
		return
	}
	if err != nil {
		serverError(w, "reset password", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"owner_email": email, "temp_password": pw})
}

func (s *Server) adminPayments(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := s.store.AdminPayments(r.Context(), q.Get("tenant_id"), q.Get("status"), limit)
	if err != nil {
		serverError(w, "admin payments", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": rows})
}

func (s *Server) adminDevices(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	rows, err := s.store.AdminDevices(r.Context())
	if err != nil {
		serverError(w, "admin devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": rows})
}

func (s *Server) adminStream(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	s.serveSSE(w, r, events.AdminTopic)
}
