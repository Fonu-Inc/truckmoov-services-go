package config

import (
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServiceName                  string
	Port                         string
	NATSURL                      string
	NATSStream                   string
	DefaultTenant                string
	JWTSecret                    string
	PostgresHost                 string
	PostgresPort                 string
	PostgresUser                 string
	PostgresPassword             string
	PostgresDB                   string
	PostgresSSLMode              string
	RedisAddr                    string
	RedisDB                      string
	CORSOrigins                  []string
	MaxAccuracyM                 float64
	MaxSpeedKPH                  float64
	HistoryTTLSeconds            int
	LatestTTLSeconds             int
	GPSAPIKey                    string
	RateLimitRPS                 int
	RateLimitBurst               int
	WhatsAppAPIBaseURL           string
	WhatsAppAppID                string
	WhatsAppAppSecret            string
	WhatsAppAccountEncryptionKey string
	NotificationServiceURL       string
	DispatchServiceURL           string
	WhatsAppAccessToken          string
	WhatsAppPhoneNumberID        string
	WhatsAppBusinessAccountID    string
	WhatsAppWebhookVerifyToken   string
	SMSServiceURL                string
	SMSServiceUsername           string
	SMSServicePassword           string
	SMSServiceSenderID           string
	RouteSolverAddr              string
	ValhallaURL                  string
}

func Load(serviceName, defaultPort string) Config {
	return Config{
		ServiceName:                  serviceName,
		Port:                         getEnv(serviceName+"_PORT", defaultPort),
		NATSURL:                      getEnv("NATS_URL", "nats://localhost:4222"),
		NATSStream:                   getEnv("NATS_STREAM", "truckmoov_events"),
		DefaultTenant:                getEnv("DEFAULT_TENANT", "default"),
		JWTSecret:                    getEnv("JWT_SECRET", "replace-me"),
		PostgresHost:                 getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:                 getEnv("POSTGRES_PORT", "5432"),
		PostgresUser:                 getEnv("POSTGRES_USER", "truckmoov"),
		PostgresPassword:             getEnv("POSTGRES_PASSWORD", "truckmoov"),
		PostgresDB:                   getEnv("POSTGRES_DB", "truckmoov"),
		PostgresSSLMode:              getEnv("POSTGRES_SSLMODE", "disable"),
		RedisAddr:                    getEnv("REDIS_ADDR", "localhost:6380"),
		RedisDB:                      getEnv("REDIS_DB", "0"),
		MaxAccuracyM:                 getEnvFloat("TELEMETRY_MAX_ACCURACY_M", 500.0),
		MaxSpeedKPH:                  getEnvFloat("TELEMETRY_MAX_SPEED_KPH", 250.0),
		HistoryTTLSeconds:            getEnvInt("TRACKING_HISTORY_TTL_SECONDS", 7*24*3600),
		LatestTTLSeconds:             getEnvInt("TRACKING_LATEST_TTL_SECONDS", 30*24*3600),
		GPSAPIKey:                    getEnv("GPS_API_KEY", ""),
		RateLimitRPS:                 getEnvInt("RATE_LIMIT_RPS", 100),
		RateLimitBurst:               getEnvInt("RATE_LIMIT_BURST", 200),
		WhatsAppAPIBaseURL:           getEnv("WHATSAPP_API_BASE_URL", "https://graph.facebook.com/v18.0"),
		WhatsAppAppID:                getEnv("WHATSAPP_APP_ID", ""),
		WhatsAppAppSecret:            getEnv("WHATSAPP_APP_SECRET", ""),
		WhatsAppAccountEncryptionKey: getEnv("WHATSAPP_ACCOUNT_ENCRYPTION_KEY", ""),
		NotificationServiceURL:       getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:3003"),
		DispatchServiceURL:           getEnv("DISPATCH_SERVICE_URL", "http://localhost:3005"),
		WhatsAppAccessToken:          getEnv("WHATSAPP_ACCESS_TOKEN", ""),
		WhatsAppPhoneNumberID:        getFirstEnv([]string{"WHATSAPP_PHONE_NUMBER_ID", "WHATSAPP_BUSINESS_NUMBER_ID"}, ""),
		WhatsAppBusinessAccountID:    getFirstEnv([]string{"WHATSAPP_BUSINESS_ACCOUNT_ID", "WHATSAPP_WABA_ID"}, ""),
		WhatsAppWebhookVerifyToken:   getFirstEnv([]string{"WHATSAPP_WEBHOOK_VERIFY_TOKEN", "WEBHOOK_VERIFY_TOKEN"}, ""),
		SMSServiceURL:                getEnv("SMS_SERVICE_URL", ""),
		SMSServiceUsername:           getEnv("SMS_SERVICE_USER_NAME", ""),
		SMSServicePassword:           getEnv("SMS_SERVICE_PASSWORD", ""),
		SMSServiceSenderID:           getEnv("SMS_SERVICE_SENDER_ID", ""),
		RouteSolverAddr:              getEnv("ROUTE_SOLVER_ADDR", ""),
		ValhallaURL:                  getEnv("VALHALLA_URL", "http://valhalla:8002"),
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

func getFirstEnv(keys []string, fallback string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}

func (c Config) PostgresDSN() string {
	query := url.Values{}
	query.Set("sslmode", strings.TrimSpace(c.PostgresSSLMode))

	dsn := url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(strings.TrimSpace(c.PostgresHost), strings.TrimSpace(c.PostgresPort)),
		Path:     "/" + strings.TrimSpace(c.PostgresDB),
		RawQuery: query.Encode(),
	}

	if strings.TrimSpace(c.PostgresPassword) == "" {
		dsn.User = url.User(strings.TrimSpace(c.PostgresUser))
	} else {
		dsn.User = url.UserPassword(strings.TrimSpace(c.PostgresUser), c.PostgresPassword)
	}

	return dsn.String()
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
