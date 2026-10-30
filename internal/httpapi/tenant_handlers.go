package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fredrick-911/hotspot-platform/internal/auth"
	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/events"
	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/Fredrick-911/hotspot-platform/internal/session"
)

// tid is the tenant of the logged-in owner. Every query below filters on it:
// there is no way to name another tenant.
func tid(u *auth.User) string { return *u.TenantID }

func (s *Server) tenantOverview(w http.ResponseWriter, r *http.Request, u *auth.User) {
	o, err := s.store.TenantOverview(r.Context(), tid(u))
	if err != nil {
		serverError(w, "tenant overview", err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) tenantPayments(w http.ResponseWriter, r *http.Request, u *auth.User) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.store.Payments(r.Context(), tid(u), r.URL.Query().Get("status"), limit)
	if err != nil {
		serverError(w, "tenant payments", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": rows})
}

func (s *Server) tenantCustomers(w http.ResponseWriter, r *http.Request, u *auth.User) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.store.Customers(r.Context(), tid(u), r.URL.Query().Get("q"), limit)
	if err != nil {
		serverError(w, "tenant customers", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customers": rows})
}

// tenantRevoke ends a customer's pass now and kicks them off the router.
func (s *Server) tenantRevoke(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sess, err := session.Revoke(r.Context(), s.db, tid(u), id)
	if errors.Is(err, session.ErrNoActiveSession) {
		writeError(w, http.StatusNotFound, "that pass is not active any more")
		return
	}
	if err != nil {
		serverError(w, "revoke", err)
		return
	}

	// Best effort: the pass is already dead in RADIUS, so even if the router
	// cannot be reached the customer cannot log in again. The Reaper retries.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	kicked, derr := session.DisconnectUser(ctx, s.db, s.disconnector, sess.Username)
	resp := map[string]any{"status": "revoked", "disconnected": kicked}
	if derr != nil {
		slog.Warn("revoke: could not disconnect user", "user", sess.Username, "err", derr)
		resp["warning"] = "The pass is cancelled, but the router could not be reached to disconnect the customer right now. They will be removed shortly."
	}
	s.hub.Publish("session", map[string]string{"id": sess.ID}, events.TenantTopic(tid(u)))
	writeJSON(w, http.StatusOK, resp)
}

// ---- plans ----

func (s *Server) tenantPlans(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rows, err := s.store.Plans(r.Context(), tid(u))
	if err != nil {
		serverError(w, "tenant plans", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": rows})
}

func (s *Server) tenantCreatePlan(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in plan.Input
	if !decode(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := plan.Create(r.Context(), s.db, tid(u), in)
	if err != nil {
		serverError(w, "create plan", err)
		return
	}
	s.hub.Publish("plans", nil, events.PortalTopic(tid(u)))
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) tenantUpdatePlan(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in plan.Input
	if !decode(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := plan.Update(r.Context(), s.db, tid(u), id, in)
	if errors.Is(err, plan.ErrNotFound) {
		writeError(w, http.StatusNotFound, "plan not found")
		return
	}
	if err != nil {
		serverError(w, "update plan", err)
		return
	}
	s.hub.Publish("plans", nil, events.PortalTopic(tid(u)))
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) tenantArchivePlan(active bool) staffHandler {
	return func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		err := plan.SetActive(r.Context(), s.db, tid(u), id, active)
		if errors.Is(err, plan.ErrNotFound) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		if err != nil {
			serverError(w, "archive plan", err)
			return
		}
		s.hub.Publish("plans", nil, events.PortalTopic(tid(u)))
		writeJSON(w, http.StatusOK, map[string]bool{"active": active})
	}
}

// ---- routers ----

func (s *Server) tenantDevices(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rows, err := s.store.Devices(r.Context(), tid(u))
	if err != nil {
		serverError(w, "tenant devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": rows})
}

type deviceReq struct {
	Name         string  `json:"name"`
	MacAddress   string  `json:"mac_address"`
	NasIPAddress *string `json:"nas_ip_address"`
}

func deviceInputError(err error) (string, int) {
	switch {
	case errors.Is(err, device.ErrInvalidMAC), errors.Is(err, device.ErrInvalidIP):
		return err.Error(), http.StatusBadRequest
	case errors.Is(err, device.ErrDuplicateMAC):
		return err.Error(), http.StatusConflict
	case errors.Is(err, device.ErrDeviceNotFound):
		return "router not found", http.StatusNotFound
	}
	return "", 0
}

func (s *Server) scriptFor(d *device.Device) string {
	return d.GenerateScript(device.ScriptOptions{
		PortalHost:   s.cfg.BaseHost(),
		RadiusHost:   s.cfg.RadiusHost(),
		HeartbeatURL: s.cfg.BaseURL + "/api/device/heartbeat?id=" + d.ID,
	})
}

func (s *Server) tenantCreateDevice(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in deviceReq
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) > 100 {
		writeError(w, http.StatusBadRequest, "router name is too long")
		return
	}
	if in.NasIPAddress != nil && strings.TrimSpace(*in.NasIPAddress) == "" {
		in.NasIPAddress = nil
	}
	if in.NasIPAddress != nil {
		ip, err := device.NormalizeIP(*in.NasIPAddress)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		in.NasIPAddress = ip
	}
	d, err := device.Register(r.Context(), s.db, tid(u), in.Name, in.MacAddress, in.NasIPAddress)
	if msg, code := deviceInputError(err); code != 0 {
		writeError(w, code, msg)
		return
	}
	if err != nil {
		serverError(w, "register device", err)
		return
	}
	s.hub.Publish("device", map[string]string{"name": d.Name}, events.TenantTopic(tid(u)), events.AdminTopic)
	writeJSON(w, http.StatusCreated, map[string]any{"device": d.ID, "script": s.scriptFor(d)})
}

func (s *Server) tenantUpdateDevice(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in deviceReq
	if !decode(w, r, &in) {
		return
	}
	ip := ""
	if in.NasIPAddress != nil {
		ip = *in.NasIPAddress
	}
	d, err := device.Update(r.Context(), s.db, tid(u), id, strings.TrimSpace(in.Name), ip)
	if msg, code := deviceInputError(err); code != 0 {
		writeError(w, code, msg)
		return
	}
	if err != nil {
		serverError(w, "update device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": d.ID, "script": s.scriptFor(d)})
}

func (s *Server) tenantDeleteDevice(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := device.Delete(r.Context(), s.db, tid(u), id)
	if msg, code := deviceInputError(err); code != 0 {
		writeError(w, code, msg)
		return
	}
	if err != nil {
		serverError(w, "delete device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// tenantDeviceScript re-generates the MikroTik setup script on demand (the
// RADIUS secret never appears in list responses).
func (s *Server) tenantDeviceScript(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := device.ByID(r.Context(), s.db, tid(u), id)
	if msg, code := deviceInputError(err); code != 0 {
		writeError(w, code, msg)
		return
	}
	if err != nil {
		serverError(w, "device script", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"script": s.scriptFor(d), "name": d.Name})
}

// handleHeartbeat is what the router calls once a minute. It proves who it is
// with its RADIUS secret, sent in the POST body ("key=...").
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	key := r.PostForm.Get("key")
	if id == "" || key == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Failures from one address are throttled so the secret cannot be guessed.
	ip := s.clientIP(r)
	if ok, _ := s.loginIPLimiter.Allowed("hb|" + ip); !ok {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	err := device.Heartbeat(r.Context(), s.db, id, key)
	if errors.Is(err, device.ErrDeviceNotFound) {
		s.loginIPLimiter.Fail("hb|" + ip)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if err != nil {
		serverError(w, "heartbeat", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- announcements ----

func (s *Server) tenantAnnouncements(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rows, err := s.store.Announcements(r.Context(), tid(u), false)
	if err != nil {
		serverError(w, "announcements", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcements": rows})
}

type announcementReq struct {
	Message string `json:"message"`
	Hours   int    `json:"hours"`
}

func (s *Server) tenantCreateAnnouncement(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in announcementReq
	if !decode(w, r, &in) {
		return
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" || len([]rune(msg)) > 280 {
		writeError(w, http.StatusBadRequest, dashboard.ErrBadAnnouncement.Error())
		return
	}
	if in.Hours <= 0 {
		in.Hours = 24
	}
	if in.Hours > 24*14 {
		writeError(w, http.StatusBadRequest, "an announcement can last at most 14 days")
		return
	}
	a, err := s.store.CreateAnnouncement(r.Context(), tid(u), u.ID, msg, time.Duration(in.Hours)*time.Hour)
	if errors.Is(err, dashboard.ErrBadAnnouncement) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		serverError(w, "create announcement", err)
		return
	}
	s.hub.Publish("announcements", nil, events.PortalTopic(tid(u)))
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) tenantDeleteAnnouncement(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteAnnouncement(r.Context(), tid(u), id)
	if errors.Is(err, dashboard.ErrAnnouncementNotFound) {
		writeError(w, http.StatusNotFound, "announcement not found")
		return
	}
	if err != nil {
		serverError(w, "delete announcement", err)
		return
	}
	s.hub.Publish("announcements", nil, events.PortalTopic(tid(u)))
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) tenantStream(w http.ResponseWriter, r *http.Request, u *auth.User) {
	s.serveSSE(w, r, events.TenantTopic(tid(u)))
}
