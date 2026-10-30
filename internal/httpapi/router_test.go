package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Fredrick-911/hotspot-platform/internal/config"
)

func testHandler() http.Handler {
	return NewRouter(nil, config.Config{SessionSecret: []byte("0123456789abcdef0123456789abcdef"), BaseURL: "http://127.0.0.1:8080"})
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	testHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestHealthRejectsPost(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	req.Header.Set(csrfHeader, csrfValue)
	testHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	testHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestStateChangingCallsNeedTheCSRFHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	testHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without the header: status %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	req.Header.Set(csrfHeader, csrfValue)
	testHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with the header: status %d, want 200", rec.Code)
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	for _, p := range []string{"/static/css/theme.css", "/static/js/core.js", "/static/favicon.svg"} {
		rec := httptest.NewRecorder()
		testHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
	}
}

func TestLoginPageRendersWithoutInlineScript(t *testing.T) {
	rec := httptest.NewRecorder()
	testHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Sign in") {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, " onclick=") || strings.Contains(body, " style=") {
		t.Fatal("page contains inline script or style, which the CSP would block")
	}
}

func TestDashboardsRedirectStrangersToLogin(t *testing.T) {
	for _, p := range []string{"/admin", "/dashboard"} {
		rec := httptest.NewRecorder()
		testHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("%s: status %d location %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, p := range []string{"/api/admin/overview", "/api/tenant/overview"} {
		rec := httptest.NewRecorder()
		testHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", p, rec.Code)
		}
	}
}

func TestClientIPOnlyTrustsProxyHeaderWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")

	s := &Server{cfg: config.Config{}}
	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Errorf("untrusted proxy: got %q", got)
	}
	s.cfg.TrustProxy = true
	if got := s.clientIP(req); got != "198.51.100.7" {
		t.Errorf("trusted proxy: got %q (want the last, proxy-added entry)", got)
	}
}

func TestPaymentCallbackNeedsTheSignedToken(t *testing.T) {
	// Even with a nil database the token check runs first and rejects the call.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/payments/callback?t=forged", strings.NewReader(`{"response":{"ExternalReference":"WIFI-X","ResultCode":0,"MpesaReceiptNumber":"ABC"}}`))
	testHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestHostOnly(t *testing.T) {
	for in, want := range map[string]string{"a.example.com:8080": "a.example.com", "a.example.com": "a.example.com", "[::1]:80": "[::1]", "10.0.0.1:8081": "10.0.0.1"} {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
