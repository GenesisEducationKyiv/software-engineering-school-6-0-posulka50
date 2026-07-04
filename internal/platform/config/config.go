package config

import "os"

type Config struct {
	Port   string
	APIKey string

	DatabaseURL string

	RedisURL string

	GitHubToken string

	BrokerURL string

	BaseURL      string
	ScanInterval string
	GinMode      string

	// SagaTimeout bounds how long a Subscribe saga may stay in 'pending'
	// before the sweeper compensates it. Any reply event lost in transit
	// (notifier crash after Resend success, broker hiccup, etc.) is caught
	// here.
	SagaTimeout string
	// SagaSweepInterval is how often the sweeper scans for stuck sagas.
	SagaSweepInterval string
}

func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", "8080"),
		APIKey:       os.Getenv("API_KEY"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/github_notifier?sslmode=disable"),
		RedisURL:     getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubToken:  os.Getenv("GITHUB_TOKEN"),
		BrokerURL:    getEnv("BROKER_URL", "amqp://guest:guest@localhost:5672/"),
		BaseURL:      getEnv("BASE_URL", "http://localhost:8080"),
		ScanInterval: getEnv("SCAN_INTERVAL", "1h"),
		GinMode:      getEnv("GIN_MODE", "release"),

		SagaTimeout:       getEnv("SAGA_TIMEOUT", "5m"),
		SagaSweepInterval: getEnv("SAGA_SWEEP_INTERVAL", "30s"),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
