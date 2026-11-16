// internal/billing/intasend_test.go
package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeKenyanPhone(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"0712345678", "254712345678", false},
		{"0112345678", "254112345678", false},
		{"254712345678", "254712345678", false},
		{"712345678", "254712345678", false},
		{"+254 712 345 678", "254712345678", false},
		{"12345", "", true},
		{"", "", true},
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

func TestSendSTKPush(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/payment/mpesa-stk-push/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret_test" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "stk_123",
			"invoice": {
				"invoice_id": "INV_ABC",
				"state": "PENDING",
				"provider": "M-PESA",
				"value": 2500,
				"account": "254712345678",
				"api_ref": "sub_jan"
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient("pub_test", "secret_test")
	c.BaseURL = srv.URL

	resp, err := c.SendSTKPush(context.Background(), 2500, "0712345678", "sub_jan")
	if err != nil {
		t.Fatalf("SendSTKPush failed: %v", err)
	}

	if resp.Invoice.InvoiceID != "INV_ABC" || resp.Invoice.State != "PENDING" {
		t.Errorf("unexpected STK response: %+v", resp)
	}
}

func TestCheckPaymentStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/payment/status/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"invoice": {
				"invoice_id": "INV_ABC",
				"state": "COMPLETE",
				"provider": "M-PESA",
				"value": 2500,
				"provider_ref": "QK123456"
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient("pub_test", "secret_test")
	c.BaseURL = srv.URL

	res, err := c.CheckPaymentStatus(context.Background(), "INV_ABC")
	if err != nil {
		t.Fatalf("CheckPaymentStatus failed: %v", err)
	}

	if res.Invoice.State != "COMPLETE" {
		t.Errorf("expected COMPLETE, got %q", res.Invoice.State)
	}
}
