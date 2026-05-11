package config

import "os"

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
	GinMode      string
}

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
		GinMode:      getEnv("GIN_MODE", "release"),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
