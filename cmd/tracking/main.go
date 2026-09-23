package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/infrastructure"
	trackinghttp "github.com/truckmoov/services-go/internal/tracking/interfaces/http"
	"github.com/truckmoov/services-go/pkg/activitylog"
	"github.com/truckmoov/services-go/pkg/alert"
	"github.com/truckmoov/services-go/pkg/config"
	"github.com/truckmoov/services-go/pkg/eventbus"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/logger"
	"github.com/truckmoov/services-go/pkg/otel"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load("TRACKING", "3004")
	log := logger.New("tracking")
	log.Info("jwt_key_id", "key_id", cfg.JWTKeyID)

	if err := cfg.Validate(); err != nil {
		log.Error("config_invalid", "error", err.Error())
		os.Exit(1)
	}
	if cfg.UsingDefaultJWTSecret() {
		log.Warn("jwt_secret_default", "detail", "JWT_SECRET is unset or the default placeholder; set it before running in production")
	}
	userTokens, err := cfg.UserTokenVerifier()
	if err != nil {
		log.Error("user_token_keys_invalid", "error", err.Error())
		os.Exit(1)
	}

	shutdownTelemetry, err := otel.Setup(ctx, "tracking", log)
	if err != nil {
		log.Error("otel_setup_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		_ = shutdownTelemetry(context.Background())
	}()

	bus, err := eventbus.New(ctx, cfg.NATSURL, cfg.NATSStream, eventbus.Credentials{User: cfg.NATSUser, Password: cfg.NATSPassword}, log)
	if err != nil {
		log.Error("eventbus_init_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		_ = bus.Close()
	}()
	activityPublisher := activitylog.NewPublisher(bus)

	redisClient := redisstore.NewStore(cfg.RedisAddr, cfg.RedisDB)
	eventPublisher := infrastructure.NewEventPublisher(bus)
	clock := infrastructure.SystemClock{}
	idGenerator := infrastructure.TimeIDGenerator{}
	latestLocationStore := infrastructure.NewRedisLatestLocationRepository(redisClient, cfg.LatestTTLSeconds)
	historyStore := infrastructure.NewRedisLocationHistoryRepository(redisClient, cfg.HistoryTTLSeconds)
	imeiResolver := infrastructure.NewRedisIMEIResolver(redisClient, cfg.IMEIIdentityTTLSeconds)
	simResolver := infrastructure.NewRedisSIMResolver(redisClient, cfg.SIMTTLSeconds)

	// Self-hosted TimescaleDB backs the device-detail time-series. Connection is
	// best-effort: if it is unreachable the live path (Redis + NATS) still works and
	// telemetry history is simply disabled until it recovers.
	var telemetryStore application.TelemetryStore
	if timescaleDB, openErr := sql.Open("pgx", cfg.TimescaleDSN()); openErr != nil {
		log.Warn("timescale_open_failed", "error", openErr.Error())
	} else {
		defer func() { _ = timescaleDB.Close() }()
		if pingErr := timescaleDB.PingContext(ctx); pingErr != nil {
			log.Warn("timescale_ping_failed", "error", pingErr.Error())
		} else {
			repo := infrastructure.NewTimescaleTelemetryRepository(timescaleDB, log, 90)
			if schemaErr := repo.EnsureSchema(ctx); schemaErr != nil {
				log.Warn("timescale_schema_failed", "error", schemaErr.Error())
			} else {
				telemetryStore = repo
				log.Info("telemetry_store_ready")
			}
		}
	}

	// Keep the IMEI→{tenant,vehicle,device} cache fresh from fleet's identity events.
	consumeDeviceIdentity := application.NewConsumeDeviceIdentityUseCase(imeiResolver, log)
	if _, subErr := bus.Subscribe(ctx, "tm.*.device.identity.changed", "tracking-device-identity", consumeDeviceIdentity.Handle); subErr != nil {
		log.Warn("device_identity_subscribe_failed", "error", subErr.Error())
	}

	// Cache device→SIM from auth's SIM events so the overview tab serves SIM info
	// without a synchronous auth call.
	consumeSIMChanged := application.NewConsumeSIMChangedUseCase(simResolver, log)
	if _, subErr := bus.Subscribe(ctx, "tm.*.sim.changed", "tracking-sim-changed", consumeSIMChanged.Handle); subErr != nil {
		log.Warn("sim_changed_subscribe_failed", "error", subErr.Error())
	}

	ingestLocationUseCase := application.NewIngestLocationUseCase(
		eventPublisher,
		latestLocationStore,
		clock,
		idGenerator,
		cfg.DefaultTenant,
		historyStore,
		application.TelemetryConfig{
			MaxAccuracyM: cfg.MaxAccuracyM,
			MaxSpeedKPH:  cfg.MaxSpeedKPH,
		},
		imeiResolver,
		telemetryStore,
	)
	publishAlertUseCase := application.NewPublishAlertUseCase(
		eventPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)

	trackingQueryUseCase := application.NewTrackingQueryUseCase(
		latestLocationStore,
		historyStore,
		cfg.DefaultTenant,
	)

	// Left nil when Timescale is unavailable so the device-detail endpoints return
	// 503 (telemetry not configured) instead of masquerading empty 200s as no-data.
	var deviceTelemetryQueryUseCase *application.DeviceTelemetryQueryUseCase
	if telemetryStore != nil {
		deviceTelemetryQueryUseCase = application.NewDeviceTelemetryQueryUseCase(
			telemetryStore,
			clock,
			cfg.StatusTimeoutSeconds,
			simResolver,
		)
	}

	handler := trackinghttp.NewHandler(ingestLocationUseCase, publishAlertUseCase, trackingQueryUseCase, deviceTelemetryQueryUseCase, cfg.GPSAPIKey, userTokens, activityPublisher)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	metrics := httpx.NewMetrics(cfg.ServiceName)
	mux.Handle("/metrics", metrics.Handler())

	readiness := httpx.ReadinessHandler("tracking",
		httpx.ReadinessCheck{Name: "nats", Probe: bus.Ping},
		httpx.ReadinessCheck{Name: "redis", Probe: func(context.Context) error { return redisClient.Ping() }},
	)
	mux.Handle("/ready", readiness)
	mux.Handle("/tracking/ready", readiness)

	// Kong answers CORS in every deployed environment. SERVICE_CORS_ENABLED=true
	// turns it on here instead, which a local run without Kong needs.
	var handlerWithCORS http.Handler = metrics.Middleware(mux)
	if cfg.CORSEnabled {
		handlerWithCORS = httpx.WithCORS(handlerWithCORS, cfg.CORSOrigins)
	}
	handlerWithCORS = httpx.WithRateLimit(handlerWithCORS, cfg.RateLimitRPS, cfg.RateLimitBurst)
	handlerWithCORS = httpx.WithAuthContext(handlerWithCORS, userTokens)
	handlerWithCORS = httpx.WithErrorAlerts(handlerWithCORS, alert.NewNtfyClient(cfg.NtfyErrorTopicURL, cfg.NtfyErrorAuthToken), httpx.NewRecentRequests(3), cfg.ServiceName)

	// cmd/tracking is kept as composition root only: all business rules live in internal/tracking.
	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
