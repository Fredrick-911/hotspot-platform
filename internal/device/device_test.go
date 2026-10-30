// internal/device/device_test.go
package device

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"48:8f:5a:12:34:56", "48:8f:5a:12:34:56", false},
		{"48-8F-5A-12-34-56", "48:8f:5a:12:34:56", false},
		{"488f.5a12.3456", "48:8f:5a:12:34:56", false},
		{"not-a-mac", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeMAC(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("NormalizeMAC(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret failed: %v", err)
	}
	s2, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret failed: %v", err)
	}

	if len(s1) != 32 { // 16 bytes in hex = 32 chars
		t.Errorf("secret length = %d, want 32", len(s1))
	}
	if s1 == s2 {
		t.Errorf("expected random secrets, got identical values: %q", s1)
	}
}

func TestGenerateRouterOSScript(t *testing.T) {
	d := Device{
		MacAddress:   "48:8f:5a:12:34:56",
		RadiusSecret: "test-secret-12345",
	}

	script := d.GenerateRouterOSScript("78.141.207.74")
	if !strings.Contains(script, "secret=\"test-secret-12345\"") {
		t.Errorf("script missing radius secret")
	}
	if !strings.Contains(script, "address=78.141.207.74") {
		t.Errorf("script missing server address")
	}
	if !strings.Contains(script, "/ip hotspot profile set [find] use-radius=yes") {
		t.Errorf("script missing hotspot radius configuration")
	}
}

func TestNormalizeIP(t *testing.T) {
	good := map[string]string{"10.8.0.2": "10.8.0.2", " 10.8.0.2 ": "10.8.0.2"}
	for in, want := range good {
		got, err := NormalizeIP(in)
		if err != nil || got == nil || *got != want {
			t.Errorf("NormalizeIP(%q) = %v, %v; want %q", in, got, err, want)
		}
	}
	if got, err := NormalizeIP("  "); err != nil || got != nil {
		t.Errorf("empty IP should mean 'not set', got %v, %v", got, err)
	}
	for _, bad := range []string{"not-an-ip", "127.0.0.1", "0.0.0.0", "::1", "224.0.0.1", "10.8.0"} {
		if _, err := NormalizeIP(bad); err == nil {
			t.Errorf("NormalizeIP(%q) should fail", bad)
		}
	}
}

func TestOnline(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-time.Minute), now.Add(-10*time.Minute)
	if !(Device{LastSeenAt: &recent}).Online(now) {
		t.Error("a router seen a minute ago should be online")
	}
	if (Device{LastSeenAt: &old}).Online(now) || (Device{}).Online(now) {
		t.Error("an old or never-seen router should be offline")
	}
}

func TestGenerateScriptHeartbeatAndPAP(t *testing.T) {
	d := Device{ID: "abc", Name: "Kamau shop", MacAddress: "48:8f:5a:12:34:56", RadiusSecret: "sekrit"}
	s := d.GenerateScript(ScriptOptions{PortalHost: "kamau.example.com", RadiusHost: "10.8.0.1", HeartbeatURL: "http://kamau.example.com/api/device/heartbeat?id=abc"})
	for _, want := range []string{
		`address=10.8.0.1 secret="sekrit"`,
		"login-by=cookie,http-pap",
		"dst-host=kamau.example.com",
		"hotspot-heartbeat",
		"Kamau shop",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script is missing %q", want)
		}
	}
	if strings.Contains(d.GenerateScript(ScriptOptions{PortalHost: "x"}), "heartbeat") {
		t.Error("no heartbeat URL means no heartbeat scheduler")
	}
}
