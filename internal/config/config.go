package config

import (
	"fmt"
	"os"
	"strconv"
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

	return Config{
		Env:              getenv("APP_ENV", "development"),
		HTTPPort:         port,
		DatabaseURL:      dbURL,
		PayHeroUsername:  getenv("PAYHERO_API_USERNAME", ""),
		PayHeroPassword:  getenv("PAYHERO_API_PASSWORD", ""),
		PayHeroChannelID: channelID,
		BaseURL:          getenv("APP_BASE_URL", "http://127.0.0.1:8080"),
	}, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
