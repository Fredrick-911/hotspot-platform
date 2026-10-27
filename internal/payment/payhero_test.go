// internal/payment/payhero_test.go
package payment

import "testing"

func TestNormalizeKenyanPhone(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"0712345678", "0712345678", false},
		{"254712345678", "0712345678", false},
		{"+254712345678", "0712345678", false},
		{"0112345678", "0112345678", false},
		{"+254 712 345 678", "0712345678", false},
		{"12345", "", true},
		{"not-a-number", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeKenyanPhone(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("NormalizeKenyanPhone(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeKenyanPhone(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
