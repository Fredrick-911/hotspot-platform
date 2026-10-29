// internal/httpapi/dashboard_handlers.go
package httpapi

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Fredrick-911/hotspot-platform/internal/dashboard"
	"github.com/Fredrick-911/hotspot-platform/internal/device"
	"github.com/Fredrick-911/hotspot-platform/internal/plan"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

var dashboardTemplate = template.Must(template.ParseFS(templateFS, "templates/dashboard.html"))

func handleDashboard(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())
	searchQuery := r.URL.Query().Get("q")

	stats, err := dashboard.GetStats(r.Context(), db, t.ID)
	if err != nil {
		slog.Error("failed to get dashboard stats", "tenant_id", t.ID, "err", err)
	}

	sessions, err := dashboard.ListCustomerSessions(r.Context(), db, t.ID, searchQuery)
	if err != nil {
		slog.Error("failed to list customer sessions", "tenant_id", t.ID, "err", err)
	}

	plans, err := plan.ListByTenant(r.Context(), db, t.ID)
	if err != nil {
		slog.Error("failed to list plans", "tenant_id", t.ID, "err", err)
	}

	devices, err := device.ListByTenant(r.Context(), db, t.ID)
	if err != nil {
		slog.Error("failed to list devices", "tenant_id", t.ID, "err", err)
	}

	settings, err := dashboard.GetTenantSettings(r.Context(), db, t.ID)
	if err != nil {
		slog.Error("failed to get tenant settings", "tenant_id", t.ID, "err", err)
		settings = dashboard.TenantSettings{Name: t.Name}
	}

	serverHost := r.Host
	if colonIdx := strings.Index(serverHost, ":"); colonIdx != -1 {
		serverHost = serverHost[:colonIdx]
	}

	data := dashboard.DashboardViewData{
		TenantName:  t.Name,
		Subdomain:   t.Subdomain,
		Settings:    settings,
		Stats:       stats,
		Sessions:    sessions,
		Plans:       plans,
		Devices:     devices,
		SearchQuery: searchQuery,
		ServerHost:  serverHost,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTemplate.Execute(w, data); err != nil {
		slog.Error("dashboard template render failed", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func handleExportCSV(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	csvData, err := dashboard.GenerateCSVExport(r.Context(), db, t.ID)
	if err != nil {
		slog.Error("failed to export CSV", "tenant_id", t.ID, "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("%s-transactions-%s.csv", t.Subdomain, timeNowFormatted())
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	_, _ = w.Write(csvData)
}

func timeNowFormatted() string {
	return strconv.FormatInt(1000, 10)
}

func handleUpdateSettings(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	channelID, _ := strconv.Atoi(r.FormValue("channel_id"))
	err := dashboard.UpdateTenantSettings(
		r.Context(),
		db,
		t.ID,
		r.FormValue("name"),
		r.FormValue("support_phone"),
		channelID,
	)
	if err != nil {
		slog.Error("failed to update tenant settings", "tenant_id", t.ID, "err", err)
	}

	http.Redirect(w, r, "/dashboard?tenant="+t.Subdomain, http.StatusSeeOther)
}

func handleRevokeSession(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	sessionID := r.FormValue("session_id")
	if err := dashboard.RevokeSession(r.Context(), db, t.ID, sessionID); err != nil {
		slog.Error("failed to revoke session", "tenant_id", t.ID, "err", err)
	}

	http.Redirect(w, r, "/dashboard?tenant="+t.Subdomain, http.StatusSeeOther)
}

func handleCreatePlan(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	priceKES, _ := strconv.ParseFloat(r.FormValue("price"), 64)
	duration, _ := strconv.Atoi(r.FormValue("duration"))
	dataMB, _ := strconv.ParseInt(r.FormValue("data_mb"), 10, 64)

	input := dashboard.CreatePlanInput{
		Name:            strings.TrimSpace(r.FormValue("name")),
		PriceKES:        priceKES,
		DurationMinutes: duration,
		SpeedLimit:      r.FormValue("speed_limit"),
		DataLimitMB:     dataMB,
	}

	if err := dashboard.CreatePlan(r.Context(), db, t.ID, input); err != nil {
		slog.Error("failed to create plan", "tenant_id", t.ID, "err", err)
	}

	http.Redirect(w, r, "/dashboard?tenant="+t.Subdomain, http.StatusSeeOther)
}

func handleDeletePlan(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	planID := r.FormValue("plan_id")
	if err := dashboard.DeletePlan(r.Context(), db, t.ID, planID); err != nil {
		slog.Error("failed to delete plan", "tenant_id", t.ID, "err", err)
	}

	http.Redirect(w, r, "/dashboard?tenant="+t.Subdomain, http.StatusSeeOther)
}
