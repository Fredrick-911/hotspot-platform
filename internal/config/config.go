package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings for the API.
type Config struct {
	Env              string
	HTTPPort         int
	DatabaseURL      string
	PayHeroUsername  string
	PayHeroPassword  string
	PayHeroChannelID int
	BaseURL          string // e.g. "http://78.141.207.74:8081"

	// Dashboards and sessions.
	SessionSecret    []byte // signs login cookies and the PayHero callback token
	TrustProxy       bool   // believe X-Forwarded-For/Proto (only behind Caddy/nginx)
	AllowTenantQuery bool   // allow ?tenant=name on the portal (turn off once wildcard DNS works)
	Timezone         string // business time zone for "today" and charts
	AutoMigrate      bool   // apply migrations/*.sql at startup

	// First platform admin, created at startup if no admin exists yet.
	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	// What the generated MikroTik script points at.
	RadiusPublicHost string // address of FreeRADIUS as the router reaches it (WireGuard IP); default = BaseURL host
}

// Load reads settings from the environment.
func Load() (Config, error) {
	port, err := strconv.Atoi(getenv("HTTP_PORT", "8080"))
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_PORT must be a number: %w", err)
	}

	dbURL := getenv("DATABASE_URL", "")
	if dbURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	channelID, _ := strconv.Atoi(getenv("PAYHERO_CHANNEL_ID", "0"))

	cfg := Config{
		Env:                    getenv("APP_ENV", "development"),
		HTTPPort:               port,
		DatabaseURL:            dbURL,
		PayHeroUsername:        getenv("PAYHERO_API_USERNAME", ""),
		PayHeroPassword:        getenv("PAYHERO_API_PASSWORD", ""),
		PayHeroChannelID:       channelID,
		BaseURL:                strings.TrimRight(getenv("APP_BASE_URL", "http://127.0.0.1:8080"), "/"),
		TrustProxy:             getbool("TRUST_PROXY", false),
		AllowTenantQuery:       getbool("ALLOW_TENANT_QUERY", true),
		Timezone:               getenv("APP_TIMEZONE", "Africa/Nairobi"),
		AutoMigrate:            getbool("AUTO_MIGRATE", true),
		BootstrapAdminEmail:    getenv("BOOTSTRAP_ADMIN_EMAIL", ""),
		BootstrapAdminPassword: getenv("BOOTSTRAP_ADMIN_PASSWORD", ""),
		RadiusPublicHost:       getenv("RADIUS_PUBLIC_HOST", ""),
	}

	secret := getenv("SESSION_SECRET", "")
	switch {
	case len(secret) >= 32:
		cfg.SessionSecret = []byte(secret)
	case cfg.Env == "development":
		// A random key per start: fine for development, but everyone is logged out on restart.
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return Config{}, err
		}
		cfg.SessionSecret = buf
		slog.Warn("SESSION_SECRET is not set (needs 32+ characters): using a temporary key, logins reset on every restart")
	default:
		return Config{}, fmt.Errorf("SESSION_SECRET must be set to at least 32 characters when APP_ENV=%s (try: openssl rand -hex 32)", cfg.Env)
	}
	return cfg, nil
}

// CookieSecure reports whether cookies should carry the Secure flag.
func (c Config) CookieSecure() bool { return strings.HasPrefix(c.BaseURL, "https://") }

// PayHeroConfigured reports whether real M-Pesa payments are switched on.
func (c Config) PayHeroConfigured() bool { return c.PayHeroUsername != "" && c.PayHeroChannelID > 0 }

// DevPayments reports whether the "simulate a successful payment" shortcut is allowed:
// development mode AND no real PayHero credentials.
func (c Config) DevPayments() bool { return c.Env == "development" && !c.PayHeroConfigured() }

// RadiusHost is the address the generated router script uses for RADIUS.
func (c Config) RadiusHost() string {
	if c.RadiusPublicHost != "" {
		return c.RadiusPublicHost
	}
	return c.BaseHost()
}

// BaseHost is the host part of APP_BASE_URL, without port.
func (c Config) BaseHost() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" {
		return "127.0.0.1"
	}
	return u.Hostname()
}

// Fingerprint is a short, non-secret ID of the signing key (for logs).
func (c Config) Fingerprint() string {
	sum := sha256.Sum256(c.SessionSecret)
	return hex.EncodeToString(sum[:4])
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getbool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
