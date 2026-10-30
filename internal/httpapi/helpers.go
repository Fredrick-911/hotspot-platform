package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var safeNameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

const maxBody = 64 << 10 // 64 KB is plenty for any JSON we accept

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// serverError logs the real cause and tells the browser only that it failed.
func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "something went wrong on our side")
}

// decode reads a JSON body (size-limited, unknown fields rejected).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "empty request")
		default:
			writeError(w, http.StatusBadRequest, "invalid request")
		}
		return false
	}
	return true
}

func setCookie(w http.ResponseWriter, name, value string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// tenantQuery returns "?tenant=x" when the page was opened that way, so links
// and API calls on the portal keep working before wildcard DNS exists.
func tenantQuery(r *http.Request, allow bool) string {
	if allow {
		t := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tenant")))
		if t != "" && safeNameRE.MatchString(t) {
			return "?tenant=" + t
		}
	}
	return ""
}

// pathID returns the {id} path value if it is a UUID; otherwise it answers 404.
func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "not found")
		return "", false
	}
	return id, true
}

// isUUID is true for the canonical 36-character form only; Postgres would also
// accept braces or URN prefixes, which we never produce.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}
