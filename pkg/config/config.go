package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServiceName       string
	Port              string
	NATSURL           string
	NATSStream        string
	DefaultTenant     string
	RedisAddr         string
	RedisDB           string
	CORSOrigins       []string
	MaxAccuracyM      float64
	MaxSpeedKPH       float64
	HistoryTTLSeconds int
	LatestTTLSeconds  int
	GPSAPIKey         string
	RateLimitRPS      int
	RateLimitBurst    int
}

func Load(serviceName, defaultPort string) Config {
	return Config{
		ServiceName:       serviceName,
		Port:              getEnv(serviceName+"_PORT", defaultPort),
		NATSURL:           getEnv("NATS_URL", "nats://localhost:4222"),
		NATSStream:        getEnv("NATS_STREAM", "truckmoov.events"),
		DefaultTenant:     getEnv("DEFAULT_TENANT", "default"),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6380"),
		RedisDB:           getEnv("REDIS_DB", "0"),
		MaxAccuracyM:      getEnvFloat("TELEMETRY_MAX_ACCURACY_M", 500.0),
		MaxSpeedKPH:       getEnvFloat("TELEMETRY_MAX_SPEED_KPH", 250.0),
		HistoryTTLSeconds: getEnvInt("TRACKING_HISTORY_TTL_SECONDS", 7*24*3600),
		LatestTTLSeconds:  getEnvInt("TRACKING_LATEST_TTL_SECONDS", 30*24*3600),
		GPSAPIKey:      getEnv("GPS_API_KEY", ""),
		RateLimitRPS:   getEnvInt("RATE_LIMIT_RPS", 100),
		RateLimitBurst: getEnvInt("RATE_LIMIT_BURST", 200),
		CORSOrigins: parseCSVEnv(
			"CORS_ALLOWED_ORIGINS",
			"http://localhost:5173,http://127.0.0.1:5173,http://localhost:8000,http://127.0.0.1:8000",
		),
	}
}

func getEnvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
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
