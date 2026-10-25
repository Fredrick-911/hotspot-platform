// internal/device/device_test.go
package device

import (
	"strings"
	"testing"
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
