package config

import (
	"os"
	"strings"
)

type Config struct {
	ServiceName   string
	Port          string
	NATSURL       string
	NATSStream    string
	DefaultTenant string
	RedisAddr     string
	RedisDB       string
	CORSOrigins   []string
}

func Load(serviceName, defaultPort string) Config {
	return Config{
		ServiceName:   serviceName,
		Port:          getEnv(serviceName+"_PORT", defaultPort),
		NATSURL:       getEnv("NATS_URL", "nats://localhost:4222"),
		NATSStream:    getEnv("NATS_STREAM", "truckmoov.events"),
		DefaultTenant: getEnv("DEFAULT_TENANT", "default"),
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6380"),
		RedisDB:       getEnv("REDIS_DB", "0"),
		CORSOrigins: parseCSVEnv(
			"CORS_ALLOWED_ORIGINS",
			"http://localhost:5173,http://127.0.0.1:5173,http://localhost:8000,http://127.0.0.1:8000",
		),
	}
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func parseCSVEnv(key, fallback string) []string {
	value := getEnv(key, fallback)
	parts := strings.Split(value, ",")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
	}

	return filtered
}
