package config

import "os"

// Config holds all application configuration loaded from environment variables.
type Config struct {
	Port   string
	APIKey string

	DatabaseURL string

	RedisURL string

	GitHubToken string

	ResendAPIKey string
	EmailFrom    string

	BaseURL      string
	ScanInterval string
}

// Load reads configuration from environment variables and returns a Config with defaults applied.
func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", "8080"),
		APIKey:       os.Getenv("API_KEY"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/github_notifier?sslmode=disable"),
		RedisURL:     getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubToken:  os.Getenv("GITHUB_TOKEN"),
		ResendAPIKey: os.Getenv("RESEND_API_KEY"),
		EmailFrom:    os.Getenv("EMAIL_FROM"),
		BaseURL:      getEnv("BASE_URL", "http://localhost:8080"),
		ScanInterval: getEnv("SCAN_INTERVAL", "1h"),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
