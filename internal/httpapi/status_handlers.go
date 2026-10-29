// internal/httpapi/status_handlers.go
package httpapi

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Fredrick-911/hotspot-platform/internal/session"
	"github.com/Fredrick-911/hotspot-platform/internal/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

var statusTemplate = template.Must(template.ParseFS(templateFS, "templates/status.html"))

type statusViewData struct {
	Tenant *tenant.Tenant
	Detail *session.ActiveSessionDetail
}

func handleStatusPage(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	t := tenant.FromContext(r.Context())

	// Lookup query: query param ?q=, or ?mac=, or ?phone=
	queryVal := r.URL.Query().Get("q")
	if queryVal == "" {
		queryVal = r.URL.Query().Get("phone")
	}
	if queryVal == "" {
		queryVal = r.URL.Query().Get("mac")
	}

	var detail *session.ActiveSessionDetail
	if strings.TrimSpace(queryVal) != "" && db != nil {
		var err error
		detail, err = session.GetActiveSessionDetail(r.Context(), db, t.ID, queryVal)
		if err != nil {
			slog.Debug("no active session found for query", "query", queryVal, "err", err)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statusTemplate.Execute(w, statusViewData{Tenant: t, Detail: detail}); err != nil {
		slog.Error("status template render failed", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
