package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime settings for the API.
type Config struct {
	Env         string // "development" or "production"
	HTTPPort    int
	DatabaseURL string // Add this
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

	return Config{
		Env:         getenv("APP_ENV", "development"),
		HTTPPort:    port,
		DatabaseURL: dbURL,
	}, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
