// internal/dashboard/dashboard_test.go
package dashboard

import (
	"testing"
	"time"
)

func TestCustomerSession_TimeRemaining(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		session   CustomerSession
		wantMatch string
	}{
		{
			name: "Active with minutes remaining",
			session: CustomerSession{
				Status:    "ACTIVE",
				ExpiresAt: now.Add(45 * time.Minute),
			},
			wantMatch: "45m left",
		},
		{
			name: "Active with hours remaining",
			session: CustomerSession{
				Status:    "ACTIVE",
				ExpiresAt: now.Add(2*time.Hour + 10*time.Minute),
			},
			wantMatch: "2h 10m left",
		},
		{
			name: "Expired session in past",
			session: CustomerSession{
				Status:    "ACTIVE",
				ExpiresAt: now.Add(-5 * time.Minute),
			},
			wantMatch: "Expired",
		},
		{
			name: "Revoked session",
			session: CustomerSession{
				Status:    "EXPIRED",
				ExpiresAt: now.Add(2 * time.Hour),
			},
			wantMatch: "Expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.session.TimeRemaining()
			if got != tt.wantMatch {
				t.Errorf("TimeRemaining() = %v, want %v", got, tt.wantMatch)
			}
		})
	}
}

func TestStats_FormattedRevenue(t *testing.T) {
	s := Stats{TodayRevenueCents: 25000}
	if got := s.FormattedRevenue(); got != "250.00" {
		t.Errorf("FormattedRevenue() = %v, want 250.00", got)
	}
}
