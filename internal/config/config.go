// Package config loads application settings from environment variables.
//
// Every setting has a safe development default, so `make run` works with no
// setup. In production the values come from the server's environment.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime settings for the API.
type Config struct {
	Env      string // "development" or "production"
	HTTPPort int
}

// Load reads settings from the environment.
func Load() (Config, error) {
	port, err := strconv.Atoi(getenv("HTTP_PORT", "8080"))
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_PORT must be a number: %w", err)
	}
	return Config{
		Env:      getenv("APP_ENV", "development"),
		HTTPPort: port,
	}, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
