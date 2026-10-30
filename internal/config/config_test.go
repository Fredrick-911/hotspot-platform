package config

import "testing"

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error without DATABASE_URL")
	}
}

func TestProductionNeedsASessionSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_SECRET", "too-short")
	if _, err := Load(); err == nil {
		t.Fatal("a short SESSION_SECRET must be refused in production")
	}
	t.Setenv("SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	cfg, err := Load()
	if err != nil || len(cfg.SessionSecret) != 32 {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
}

func TestDevelopmentGetsATemporarySecretAndDevPayments(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("APP_ENV", "development")
	t.Setenv("SESSION_SECRET", "")
	t.Setenv("PAYHERO_API_USERNAME", "")
	cfg, err := Load()
	if err != nil || len(cfg.SessionSecret) != 32 {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
	if !cfg.DevPayments() {
		t.Fatal("dev mode without PayHero credentials should allow simulated payments")
	}
	t.Setenv("PAYHERO_API_USERNAME", "u")
	t.Setenv("PAYHERO_CHANNEL_ID", "12")
	if cfg, _ = Load(); cfg.DevPayments() {
		t.Fatal("simulated payments must be off once PayHero is configured")
	}
}

func TestBaseHostAndSecureCookie(t *testing.T) {
	c := Config{BaseURL: "https://wifi.example.com:8443"}
	if c.BaseHost() != "wifi.example.com" || !c.CookieSecure() || c.RadiusHost() != "wifi.example.com" {
		t.Fatalf("%+v", c)
	}
	c = Config{BaseURL: "http://78.141.207.74:8081", RadiusPublicHost: "10.8.0.1"}
	if c.CookieSecure() || c.RadiusHost() != "10.8.0.1" {
		t.Fatalf("%+v", c)
	}
}
