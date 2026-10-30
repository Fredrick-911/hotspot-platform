package httpapi

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// csrfHeader must be present on every browser call that changes data. A
// cross-site form cannot set a custom header, and a cross-site fetch() that
// tries needs a CORS pre-flight that we never approve.
const (
	csrfHeader = "X-Requested-With"
	csrfValue  = "hotspot"
)

// statusRecorder remembers the status code for the access log. Unwrap and
// Flush keep Server-Sent Events working through it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// chain wraps h with the standard middleware, outermost first.
func (s *Server) chain(h http.Handler) http.Handler {
	return s.recoverer(s.accessLog(s.securityHeaders(s.csrf(h))))
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity on purpose
					panic(v)
				}
				slog.Error("panic in handler", "path", r.URL.Path, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "something went wrong on our side")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/stream") { // long-lived: logged when it opens would be noise
			return
		}
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds(), "ip", s.clientIP(r))
	})
}

// Content-Security-Policy. No inline scripts or styles anywhere, no third-party
// hosts: the captive portal must work inside the router's walled garden, and
// an injected script must have nowhere to run or to send data.
const cspBase = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		csp := cspBase
		// The customer portal posts the login form to the router's own address
		// (link-login), so it cannot pin form-action. Everything else can.
		if !isPortalPath(r.URL.Path) {
			csp += "; form-action 'self'"
		}
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if s.cfg.CookieSecure() {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func isPortalPath(p string) bool { return p == "/" }

// csrf rejects state-changing browser calls that lack the custom header.
// The PayHero webhook and the router heartbeat are not browser calls; they
// have their own proofs (a signed token, the router's secret).
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.URL.Path != "/api/payments/callback" && r.URL.Path != "/api/device/heartbeat" &&
				r.Header.Get(csrfHeader) != csrfValue {
				writeError(w, http.StatusForbidden, "missing request header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the caller's address. Forwarding headers are believed only when
// TRUST_PROXY=true (the API sits behind Caddy/nginx); otherwise anyone could
// pick their own IP and dodge the rate limits.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// The proxy appends the address it saw as the LAST entry.
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
