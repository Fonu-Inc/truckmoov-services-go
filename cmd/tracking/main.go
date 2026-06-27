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

	shutdownTelemetry, err := otel.Setup(ctx, "tracking", log)
	if err != nil {
		log.Error("otel_setup_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		_ = shutdownTelemetry(context.Background())
	}()

	bus, err := eventbus.New(ctx, cfg.NATSURL, cfg.NATSStream, log)
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
		)
	}

	handler := trackinghttp.NewHandler(ingestLocationUseCase, publishAlertUseCase, trackingQueryUseCase, deviceTelemetryQueryUseCase, cfg.GPSAPIKey, cfg.JWTSecret, activityPublisher)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	handlerWithCORS := httpx.WithCORS(mux, cfg.CORSOrigins)
	handlerWithCORS = httpx.WithRateLimit(handlerWithCORS, cfg.RateLimitRPS, cfg.RateLimitBurst)
	handlerWithCORS = httpx.WithAuthContext(handlerWithCORS, cfg.JWTSecret)

	// cmd/tracking is kept as composition root only: all business rules live in internal/tracking.
	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
