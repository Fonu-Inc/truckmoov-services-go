package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/pkg/identity"
)

// defaultJWTSecret is the placeholder secret used for local/dev runs. It is
// rejected at startup in production (see Config.Validate).
const defaultJWTSecret = "replace-me"

type Config struct {
	ServiceName string
	// Environment is the effective deployment environment (e.g. "production",
	// "development"), resolved from APP_ENV/ENV/GO_ENV. Drives fail-closed
	// validation.
	Environment string
	Port        string
	NATSURL     string
	NATSStream  string
	// NATSUser and NATSPassword identify this service to the event bus, which
	// limits what each service may publish. Empty connects anonymously.
	NATSUser      string
	NATSPassword  string
	DefaultTenant string
	JWTSecret     string
	// UserTokenPublicKeys verify user tokens; only auth holds the signing key.
	// While LegacyTokensAccepted is on, shared-secret tokens signed with
	// JWTSecret are accepted too, until tokens issued before the switch expire.
	UserTokenPublicKeys  string
	LegacyTokensAccepted bool
	// ServiceTokenDir holds one Kubernetes service account token per service
	// this one calls. ServiceAuthDevSecret stands in for them outside the
	// cluster only.
	ServiceTokenDir      string
	ServiceAuthDevSecret string
	// PortalEnforced refuses requests from outside a route's audience; off,
	// they are only logged, to find any caller the route table missed.
	PortalEnforced bool
	// PermissionsEnforced refuses users lacking a route's permission; off,
	// they are only logged.
	PermissionsEnforced bool
	// JWTKeyID is an arbitrary label rotated alongside JWT_SECRET (not
	// derived from it) — safe to log at startup so two services' logs can
	// be diffed to confirm they're on the same secret generation, without
	// ever deriving anything from the secret material itself.
	JWTKeyID          string
	PostgresHost      string
	PostgresPort      string
	PostgresUser      string
	PostgresPassword  string
	PostgresDB        string
	PostgresSSLMode   string
	TimescaleHost     string
	TimescalePort     string
	TimescaleUser     string
	TimescalePassword string
	TimescaleDB       string
	TimescaleSSLMode  string
	RedisAddr         string
	RedisDB           string
	CORSOrigins       []string
	// CORSEnabled is false in every deployed environment: Kong answers CORS
	// there, and two layers both setting Access-Control-Allow-Origin makes a
	// browser reject the response for carrying two values.
	CORSEnabled               bool
	MaxAccuracyM              float64
	MaxSpeedKPH               float64
	HistoryTTLSeconds         int
	LatestTTLSeconds          int
	IMEIIdentityTTLSeconds    int
	SIMTTLSeconds             int
	TrackingGateEnabled       bool
	TrackingGateReloadSeconds int
	StatusTimeoutSeconds      int
	GPSAPIKey                 string
	RateLimitRPS              int
	RateLimitBurst            int
	WhatsAppAPIBaseURL        string
	WhatsAppAppID             string
	EventSigningSecret        string
	EventAcceptUnsigned       bool
	WhatsAppAppSecret         string
	// Language a campaign template is sent in. Meta resolves a template by name
	// and language together, so the wrong one is a template that does not exist.
	WhatsAppTemplateLanguage     string
	WhatsAppAccountEncryptionKey string
	NotificationServiceURL       string
	DispatchServiceURL           string
	AuthServiceURL               string
	WhatsAppAccessToken          string
	WhatsAppPhoneNumberID        string
	WhatsAppBusinessAccountID    string
	WhatsAppWebhookVerifyToken   string
	DotgoBaseURL                 string
	DotgoAccountID               string
	DotgoAPIToken                string
	DotgoSenderMask              string
	DotgoCallbackURL             string
	DotgoWebhookSecret           string
	SMTPHost                     string
	SMTPPort                     int
	SMTPUsername                 string
	SMTPPassword                 string
	SMTPFromAddress              string
	SMTPFromName                 string
	RouteSolverAddr              string
	// RouteSolverAuthToken, when set, is sent as a bearer token in gRPC call
	// metadata on every route-solver request. Empty by default — matches
	// the route-solver service's own rollout-safety default (see its
	// config.py), since both sides need this provisioned identically before
	// enforcement is safe to turn on.
	RouteSolverAuthToken string
	ValhallaURL          string
	// How long a computed overview-map route line is reused before Valhalla is
	// asked again. Stops rarely change mid-job, so this is a cache, not a limit.
	ActiveJobsMapCacheSeconds int
	NtfyErrorTopicURL         string
	NtfyErrorAuthToken        string
	FleetServiceURL           string

	// Road matching: which road each moving fix was on, via Valhalla.
	RoadMatchEnabled            bool
	RoadMatchMinSpeedKPH        float64
	RoadMatchTracePoints        int
	RoadMatchMaxTraceAgeSeconds int
	RoadMatchTimeoutSeconds     int
	RoadMatchConcurrency        int
}

func Load(serviceName, defaultPort string) Config {
	return Config{
		ServiceName:                  serviceName,
		Environment:                  normalizeEnv(getFirstEnv([]string{"APP_ENV", "ENV", "GO_ENV"}, "development")),
		Port:                         getEnv(serviceName+"_PORT", defaultPort),
		NATSURL:                      getEnv("NATS_URL", "nats://localhost:4222"),
		NATSStream:                   getEnv("NATS_STREAM", "truckmoov_events"),
		NATSUser:                     getEnv("NATS_USER", ""),
		NATSPassword:                 getEnv("NATS_PASSWORD", ""),
		DefaultTenant:                getEnv("DEFAULT_TENANT", "default"),
		JWTSecret:                    getEnv("JWT_SECRET", "replace-me"),
		UserTokenPublicKeys:          getEnv("USER_TOKEN_PUBLIC_KEYS", ""),
		LegacyTokensAccepted:         getEnvBoolStrict("LEGACY_TOKENS_ACCEPTED", true),
		ServiceTokenDir:              getEnv("SERVICE_TOKEN_DIR", "/var/run/secrets/truckmoov/service-tokens"),
		ServiceAuthDevSecret:         getEnv("SERVICE_AUTH_DEV_SECRET", ""),
		PortalEnforced:               getEnvEnforce("PORTAL_ENFORCEMENT"),
		PermissionsEnforced:          getEnvEnforce("PERMISSION_ENFORCEMENT"),
		JWTKeyID:                     getEnv("JWT_KEY_ID", "unset"),
		PostgresHost:                 getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:                 getEnv("POSTGRES_PORT", "5432"),
		PostgresUser:                 getEnv("POSTGRES_USER", "truckmoov"),
		PostgresPassword:             getEnv("POSTGRES_PASSWORD", "truckmoov"),
		PostgresDB:                   getEnv("POSTGRES_DB", "truckmoov"),
		PostgresSSLMode:              getEnv("POSTGRES_SSLMODE", "disable"),
		TimescaleHost:                getEnv("TIMESCALE_HOST", "localhost"),
		TimescalePort:                getEnv("TIMESCALE_PORT", "5433"),
		TimescaleUser:                getEnv("TIMESCALE_USER", "truckmoov"),
		TimescalePassword:            getEnv("TIMESCALE_PASSWORD", "truckmoov"),
		TimescaleDB:                  getEnv("TIMESCALE_DB", "telemetry"),
		TimescaleSSLMode:             getEnv("TIMESCALE_SSLMODE", "disable"),
		RedisAddr:                    getEnv("REDIS_ADDR", "localhost:6380"),
		RedisDB:                      getEnv("REDIS_DB", "0"),
		MaxAccuracyM:                 getEnvFloat("TELEMETRY_MAX_ACCURACY_M", 500.0),
		MaxSpeedKPH:                  getEnvFloat("TELEMETRY_MAX_SPEED_KPH", 250.0),
		HistoryTTLSeconds:            getEnvInt("TRACKING_HISTORY_TTL_SECONDS", 7*24*3600),
		RoadMatchEnabled:             getEnvBoolStrict("TRACKING_ROAD_MATCH_ENABLED", false),
		RoadMatchMinSpeedKPH:         getEnvFloat("TRACKING_ROAD_MATCH_MIN_SPEED_KPH", 20),
		RoadMatchTracePoints:         getEnvInt("TRACKING_ROAD_MATCH_TRACE_POINTS", 8),
		RoadMatchMaxTraceAgeSeconds:  getEnvInt("TRACKING_ROAD_MATCH_MAX_TRACE_AGE_SECONDS", 180),
		RoadMatchTimeoutSeconds:      getEnvInt("TRACKING_ROAD_MATCH_TIMEOUT_SECONDS", 3),
		RoadMatchConcurrency:         getEnvInt("TRACKING_ROAD_MATCH_CONCURRENCY", 4),
		LatestTTLSeconds:             getEnvInt("TRACKING_LATEST_TTL_SECONDS", 30*24*3600),
		IMEIIdentityTTLSeconds:       getEnvInt("TRACKING_IMEI_IDENTITY_TTL_SECONDS", 90*24*3600),
		SIMTTLSeconds:                getEnvInt("TRACKING_SIM_TTL_SECONDS", 90*24*3600),
		TrackingGateEnabled:          getEnvBoolStrict("TRACKING_GATE_ENABLED", true),
		TrackingGateReloadSeconds:    getEnvInt("TRACKING_GATE_RELOAD_SECONDS", 60),
		StatusTimeoutSeconds:         getEnvInt("TRACKING_STATUS_TIMEOUT_SECONDS", 600),
		GPSAPIKey:                    getEnv("GPS_API_KEY", ""),
		RateLimitRPS:                 getEnvInt("RATE_LIMIT_RPS", 100),
		RateLimitBurst:               getEnvInt("RATE_LIMIT_BURST", 200),
		WhatsAppAPIBaseURL:           getEnv("WHATSAPP_API_BASE_URL", "https://graph.facebook.com/v23.0"),
		WhatsAppAppID:                getEnv("WHATSAPP_APP_ID", ""),
		EventSigningSecret:           getEnv("EVENT_SIGNING_SECRET", ""),
		EventAcceptUnsigned:          getEnvBoolStrict("EVENT_ACCEPT_UNSIGNED", false),
		WhatsAppAppSecret:            getEnv("WHATSAPP_APP_SECRET", ""),
		WhatsAppTemplateLanguage:     getEnv("WHATSAPP_TEMPLATE_LANGUAGE", "en"),
		WhatsAppAccountEncryptionKey: getEnv("WHATSAPP_ACCOUNT_ENCRYPTION_KEY", ""),
		NotificationServiceURL:       getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:3003"),
		DispatchServiceURL:           getEnv("DISPATCH_SERVICE_URL", "http://localhost:3005"),
		// In-cluster by default, not localhost: an unset variable must not leave
		// the wallet uncharged for every message this service sends.
		AuthServiceURL:             getEnv("AUTH_SERVICE_URL", "http://auth-service:3001"),
		WhatsAppAccessToken:        getEnv("WHATSAPP_ACCESS_TOKEN", ""),
		WhatsAppPhoneNumberID:      getFirstEnv([]string{"WHATSAPP_PHONE_NUMBER_ID", "WHATSAPP_BUSINESS_NUMBER_ID"}, ""),
		WhatsAppBusinessAccountID:  getFirstEnv([]string{"WHATSAPP_BUSINESS_ACCOUNT_ID", "WHATSAPP_WABA_ID"}, ""),
		WhatsAppWebhookVerifyToken: getFirstEnv([]string{"WHATSAPP_WEBHOOK_VERIFY_TOKEN", "WEBHOOK_VERIFY_TOKEN"}, ""),
		DotgoBaseURL:               getEnv("DOTGO_BASE_URL", ""),
		DotgoAccountID:             getEnv("DOTGO_TRANSACTIONAL_ACCOUNT_ID", ""),
		DotgoAPIToken:              getEnv("DOTGO_TRANSACTIONAL_API_TOKEN", ""),
		DotgoSenderMask:            getEnv("DOTGO_TRANSACTIONAL_SENDER_MASK", ""),
		DotgoCallbackURL:           getEnv("DOTGO_CALLBACK_URL", ""),
		DotgoWebhookSecret:         getEnv("DOTGO_WEBHOOK_SECRET", ""),
		SMTPHost:                   getEnv("SMTP_HOST", ""),
		SMTPPort:                   getEnvInt("SMTP_PORT", 465),
		SMTPUsername:               getEnv("SMTP_USERNAME", ""),
		SMTPPassword:               getEnv("SMTP_PASSWORD", ""),
		// MAIL_FROM is what the auth service's mailer already reads, so both
		// services send from one configured address.
		SMTPFromAddress:           getFirstEnv([]string{"MAIL_FROM", "SMTP_FROM_ADDRESS"}, ""),
		SMTPFromName:              getEnv("SMTP_FROM_NAME", "TruckMoov"),
		RouteSolverAddr:           getEnv("ROUTE_SOLVER_ADDR", ""),
		RouteSolverAuthToken:      getEnv("ROUTE_SOLVER_AUTH_TOKEN", ""),
		ValhallaURL:               getEnv("VALHALLA_URL", "http://valhalla:8002"),
		ActiveJobsMapCacheSeconds: getEnvInt("ACTIVE_JOBS_MAP_CACHE_SECONDS", 300),
		NtfyErrorTopicURL:         getEnv("NTFY_ERROR_TOPIC_URL", ""),
		NtfyErrorAuthToken:        getEnv("NTFY_ERROR_AUTH_TOKEN", ""),
		FleetServiceURL:           getEnv("FLEET_SERVICE_URL", "http://fleet-service:3002"),
		CORSEnabled:               getEnvBoolStrict("SERVICE_CORS_ENABLED", false),
		CORSOrigins: parseCSVEnv(
			"CORS_ALLOWED_ORIGINS",
			"http://localhost:5173,http://127.0.0.1:5173,http://localhost:8000,http://127.0.0.1:8000",
		),
	}
}

func normalizeEnv(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "prod"
}

// UsingDefaultJWTSecret reports whether the JWT secret is empty or the shipped
// placeholder. Callers use it to warn (and allow) in non-production.
func (c Config) UsingDefaultJWTSecret() bool {
	secret := strings.TrimSpace(c.JWTSecret)
	return secret == "" || secret == defaultJWTSecret
}

// Validate fails closed: in production an empty or default JWT secret is a fatal
// misconfiguration and callers must refuse to boot. In non-production the same
// state is permitted so local/dev runs work without provisioning secrets.
func (c Config) Validate() error {
	if c.IsProduction() && c.UsingDefaultJWTSecret() {
		return fmt.Errorf("JWT_SECRET must be set to a non-default value in production (APP_ENV=%s)", c.Environment)
	}
	return nil
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

func getEnvEnforce(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "enforce")
}

func getEnvBoolStrict(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
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
	return buildPostgresDSN(c.PostgresHost, c.PostgresPort, c.PostgresUser, c.PostgresPassword, c.PostgresDB, c.PostgresSSLMode)
}

// TimescaleDSN points at the dedicated TimescaleDB instance (db "telemetry")
// that backs the tracking time-series, kept separate from the primary Postgres.
func (c Config) TimescaleDSN() string {
	return buildPostgresDSN(c.TimescaleHost, c.TimescalePort, c.TimescaleUser, c.TimescalePassword, c.TimescaleDB, c.TimescaleSSLMode)
}

func buildPostgresDSN(host, port, user, password, dbName, sslMode string) string {
	query := url.Values{}
	query.Set("sslmode", strings.TrimSpace(sslMode))

	dsn := url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(strings.TrimSpace(host), strings.TrimSpace(port)),
		Path:     "/" + strings.TrimSpace(dbName),
		RawQuery: query.Encode(),
	}

	if strings.TrimSpace(password) == "" {
		dsn.User = url.User(strings.TrimSpace(user))
	} else {
		dsn.User = url.UserPassword(strings.TrimSpace(user), password)
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

// UserTokenVerifier checks the user tokens this service is presented with.
func (c Config) UserTokenVerifier() (*identity.UserTokenVerifier, error) {
	legacySecret := ""
	if c.LegacyTokensAccepted {
		legacySecret = c.JWTSecret
	}
	return identity.NewUserTokenVerifier(c.UserTokenPublicKeys, legacySecret)
}

// ServiceIdentity is the name other services know this one by: the
// audience its incoming tokens carry and the account its outgoing ones name.
func (c Config) ServiceIdentity() string {
	return strings.ToLower(c.ServiceName)
}

// ServiceCaller identifies this service on its calls to others.
func (c Config) ServiceCaller() *identity.Caller {
	legacySecret := ""
	if c.LegacyTokensAccepted {
		legacySecret = c.JWTSecret
	}
	tokens := identity.NewServiceTokens(c.ServiceIdentity(), c.ServiceTokenDir, c.ServiceAuthDevSecret)
	return identity.NewCaller(c.ServiceIdentity(), tokens, legacySecret)
}

// ServiceTokenVerifier checks the services calling this one.
func (c Config) ServiceTokenVerifier() *identity.ServiceTokenVerifier {
	return identity.NewServiceTokenVerifier(c.ServiceIdentity(), c.ServiceAuthDevSecret)
}
