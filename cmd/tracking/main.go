package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/infrastructure"
	trackinghttp "github.com/truckmoov/services-go/internal/tracking/interfaces/http"
	"github.com/truckmoov/services-go/pkg/activitylog"
	"github.com/truckmoov/services-go/pkg/alert"
	"github.com/truckmoov/services-go/pkg/config"
	dbpool "github.com/truckmoov/services-go/pkg/db"
	"github.com/truckmoov/services-go/pkg/eventbus"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
	"github.com/truckmoov/services-go/pkg/logger"
	"github.com/truckmoov/services-go/pkg/otel"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
	"github.com/truckmoov/services-go/pkg/valhalla"
)

const (
	identityWarmupTimeout = 30 * time.Second
	identityRefreshEvery  = 30 * time.Minute
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
	serviceCaller := cfg.ServiceCaller()
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

	bus, err := eventbus.New(ctx, cfg.NATSURL, cfg.NATSStream, eventbus.Credentials{User: cfg.NATSUser, Password: cfg.NATSPassword}, log, eventbus.WithSigningSecret(cfg.EventSigningSecret))
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
	var quarantineStore application.QuarantineStore = infrastructure.NewInMemoryQuarantineRepository()
	if timescaleDB, openErr := sql.Open("pgx", cfg.TimescaleDSN()); openErr != nil {
		log.Warn("timescale_open_failed", "error", openErr.Error())
	} else {
		defer func() { _ = timescaleDB.Close() }()
		dbpool.ConfigurePool(timescaleDB)
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
			quarantineStore = buildQuarantineStore(ctx, timescaleDB, quarantineStore, log)
		}
	}

	gate, suspensionSync, gateMetrics := buildGate(cfg, serviceCaller, redisClient, imeiResolver, clock, log)
	identityMetrics := infrastructure.NewPrometheusIdentityMetrics(gateMetrics.Registry())
	identitySource := infrastructure.NewFleetIdentityClient(cfg.FleetServiceURL, serviceCaller)
	warmup := application.NewWarmup(clock, identityWarmupTimeout)
	identityResolver := application.NewReadThroughResolver(imeiResolver, identitySource, gate, clock, identityMetrics, log)
	identitySync := application.NewIdentitySync(identitySource, imeiResolver, gate, warmup, identityMetrics, log)

	// Keep the IMEI→{tenant,vehicle,device} cache fresh from fleet's identity events.
	quarantine := application.NewQuarantine(quarantineStore, clock).
		WithAlerter(infrastructure.NewQuarantineAlerter(bus, log)).WithLogger(log)
	go quarantine.Run(ctx)
	consumeDeviceIdentity := application.NewConsumeDeviceIdentityUseCase(identityResolver.EventSide(), log).WithQuarantine(quarantine)
	if _, subErr := bus.Subscribe(ctx, "tm.*.device.identity.changed", "tracking-device-identity", eventbus.Verified(consumeDeviceIdentity.Handle, cfg.EventSigningSecret, eventbus.VerifyOptions{AcceptUnsigned: cfg.EventAcceptUnsigned}, log)); subErr != nil {
		log.Warn("device_identity_subscribe_failed", "error", subErr.Error())
	}

	// Cache device→SIM from auth's SIM events so the overview tab serves SIM info
	// without a synchronous auth call.
	consumeSIMChanged := application.NewConsumeSIMChangedUseCase(simResolver, log)
	if _, subErr := bus.Subscribe(ctx, "tm.*.sim.changed", "tracking-sim-changed", eventbus.Verified(consumeSIMChanged.Handle, cfg.EventSigningSecret, eventbus.VerifyOptions{AcceptUnsigned: cfg.EventAcceptUnsigned}, log)); subErr != nil {
		log.Warn("sim_changed_subscribe_failed", "error", subErr.Error())
	}

	if gate != nil {
		consumeTenantSuspension := application.NewConsumeTenantSuspensionUseCase(gate)
		subscribeTenantSuspension(bus, consumeTenantSuspension, cfg.EventSigningSecret, eventbus.VerifyOptions{AcceptUnsigned: cfg.EventAcceptUnsigned}, log)
		if loadErr := suspensionSync.LoadInitial(ctx, 3, time.Second); loadErr != nil {
			log.Error("suspension_initial_load_failed", "error", loadErr.Error())
		}
		go suspensionSync.Run(ctx, time.Duration(max(cfg.TrackingGateReloadSeconds, 5))*time.Second)
	}
	go identitySync.Run(ctx, identityRefreshEvery)

	messageRate := application.NewMessageRate(clock.Now())
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
		identityResolver,
		telemetryStore,
	).WithGate(gate).WithMessageRate(messageRate).WithQuarantine(quarantine).WithWarmup(warmup).WithIdentityMetrics(identityMetrics)
	if cfg.RoadMatchEnabled {
		ingestLocationUseCase.WithRoadMatching(newRoadMatchService(cfg, historyStore, eventPublisher, clock, idGenerator, log).WithMetrics(infrastructure.NewPrometheusRoadMatchMetrics(gateMetrics.Registry())))
	}
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

	openIngest := os.Getenv("ALLOW_UNAUTHENTICATED_INGEST") == "true"
	warnIfIngestUnguarded(cfg, openIngest, log)
	handler := trackinghttp.NewHandler(ingestLocationUseCase, publishAlertUseCase, trackingQueryUseCase, deviceTelemetryQueryUseCase, cfg.GPSAPIKey, userTokens, activityPublisher).
		WithGate(gate).WithMessageRate(messageRate).WithQuarantine(quarantine).WithOpenIngest(openIngest && !cfg.IsProduction())
	accessClient := identity.NewAccessClient(cfg.AuthServiceURL, serviceCaller)
	if broadcaster, ok := bus.(eventbus.Broadcaster); ok {
		if _, subErr := broadcaster.SubscribeEveryInstance(identity.AccessChangedSubject, func(_ context.Context, e eventbus.EventEnvelope) error {
			if userID, ok := e.Data["userId"].(string); ok {
				accessClient.Forget(userID)
			}
			return nil
		}); subErr != nil {
			log.Warn("access_changed_subscribe_failed", "error", subErr.Error())
		}
	}
	mux := http.NewServeMux()
	routes := httpx.NewRoutes(mux, cfg.PortalEnforced, log, httpx.RefusalPublisher(bus, "tracking")).EnforcePermissions(cfg.PermissionsEnforced)
	handler.RegisterRoutes(routes)

	metrics := httpx.NewMetrics(cfg.ServiceName)
	metricsHandler := gateMetrics.Handler(metrics.Handler())
	routes.Handle("/metrics", []httpx.Audience{httpx.Public}, httpx.None(), metricsHandler)

	readiness := httpx.ReadinessHandler("tracking",
		httpx.ReadinessCheck{Name: "nats", Probe: bus.Ping},
		httpx.ReadinessCheck{Name: "redis", Probe: func(context.Context) error { return redisClient.Ping() }},
		httpx.ReadinessCheck{Name: "identity-snapshot", Probe: func(context.Context) error {
			if !warmup.Ready() {
				return errors.New("device identities not loaded yet")
			}
			return nil
		}},
		httpx.ReadinessCheck{Name: "suspension-gate", Probe: func(context.Context) error {
			if !gate.Ready() {
				return errors.New("suspension lists not loaded yet")
			}
			return nil
		}},
	)
	routes.Handle("/ready", []httpx.Audience{httpx.Public}, httpx.None(), readiness)
	routes.Handle("/tracking/ready", []httpx.Audience{httpx.Public}, httpx.None(), readiness)

	// Kong answers CORS in every deployed environment. SERVICE_CORS_ENABLED=true
	// turns it on here instead, which a local run without Kong needs.
	var handlerWithCORS http.Handler = metrics.Middleware(mux)
	if cfg.CORSEnabled {
		handlerWithCORS = httpx.WithCORS(handlerWithCORS, cfg.CORSOrigins)
	}
	handlerWithCORS = httpx.WithRateLimit(handlerWithCORS, cfg.RateLimitRPS, cfg.RateLimitBurst)
	handlerWithCORS = httpx.WithAuthContext(httpx.WithServiceCallers(httpx.WithCurrentAccess(handlerWithCORS, accessClient), cfg.ServiceTokenVerifier(), httpx.ServiceRoles{
		"fleet": "FLEET_ADMIN",
		"auth":  "FLEET_ADMIN",
	}), userTokens)
	handlerWithCORS = httpx.WithErrorAlerts(handlerWithCORS, alert.NewNtfyClient(cfg.NtfyErrorTopicURL, cfg.NtfyErrorAuthToken), httpx.NewRecentRequests(3), cfg.ServiceName)

	// cmd/tracking is kept as composition root only: all business rules live in internal/tracking.
	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}

func buildGate(
	cfg config.Config,
	caller *identity.Caller,
	redisClient *redisstore.Store,
	resolver application.IMEIResolver,
	clock application.Clock,
	log *slog.Logger,
) (*application.IngestGate, *application.SuspensionSync, *infrastructure.PrometheusGateMetrics) {
	metrics := infrastructure.NewPrometheusGateMetrics()
	if !cfg.TrackingGateEnabled {
		log.Warn("suspension_gate_disabled", "detail", "TRACKING_GATE_ENABLED=false; suspended customers are not blocked")
		return nil, nil, metrics
	}
	gate := application.NewIngestGate(
		infrastructure.NewRedisSuspendedSeenStore(redisClient),
		metrics,
		clock,
		log,
	)
	source := infrastructure.NewSuspensionSnapshotClient(cfg.AuthServiceURL, cfg.FleetServiceURL, caller)
	index := infrastructure.NewRedisSuspendedIMEIIndex(redisClient)
	return gate, application.NewSuspensionSync(source, gate, resolver, index, log), metrics
}

func subscribeTenantSuspension(bus eventbus.EventBus, consumer *application.ConsumeTenantSuspensionUseCase, secret string, opts eventbus.VerifyOptions, log *slog.Logger) {
	broadcaster, ok := bus.(eventbus.Broadcaster)
	if !ok {
		log.Warn("tenant_suspension_events_unavailable", "detail", "event bus cannot fan out; relying on periodic reload")
		return
	}
	if _, err := broadcaster.SubscribeEveryInstance("tm.*.tenant.suspended", eventbus.Verified(consumer.HandleSuspended, secret, opts, log)); err != nil {
		log.Warn("tenant_suspended_subscribe_failed", "error", err.Error())
	}
	if _, err := broadcaster.SubscribeEveryInstance("tm.*.tenant.reinstated", eventbus.Verified(consumer.HandleReinstated, secret, opts, log)); err != nil {
		log.Warn("tenant_reinstated_subscribe_failed", "error", err.Error())
	}
}

func buildQuarantineStore(ctx context.Context, db *sql.DB, fallback application.QuarantineStore, log *slog.Logger) application.QuarantineStore {
	repo := infrastructure.NewPostgresQuarantineRepository(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		log.Warn("quarantine_schema_failed", "error", err.Error(), "detail", "unknown devices are listed from memory only")
		return fallback
	}
	return repo
}

func warnIfIngestUnguarded(cfg config.Config, openIngest bool, log *slog.Logger) {
	if cfg.GPSAPIKey != "" {
		return
	}
	if openIngest && !cfg.IsProduction() {
		log.Warn("ingest_unauthenticated", "detail", "GPS_API_KEY is unset and ALLOW_UNAUTHENTICATED_INGEST=true; ingest is open")
		return
	}
	log.Error("gps_api_key_unset", "detail", "GPS_API_KEY is unset; all ingest is refused with 503 until it is set")
}

func newRoadMatchService(
	cfg config.Config,
	history application.LocationHistoryRepository,
	publisher application.RoadMatchPublisher,
	clock application.Clock,
	ids application.IDGenerator,
	log *slog.Logger,
) *application.RoadMatchService {
	timeout := time.Duration(cfg.RoadMatchTimeoutSeconds) * time.Second
	client := valhalla.NewClient(cfg.ValhallaURL, &http.Client{Timeout: timeout})
	return application.NewRoadMatchService(history, infrastructure.NewValhallaRoadMatcher(client), publisher, clock, ids, application.RoadMatchConfig{
		MinSpeedKPH: cfg.RoadMatchMinSpeedKPH,
		TracePoints: cfg.RoadMatchTracePoints,
		MaxTraceAge: time.Duration(cfg.RoadMatchMaxTraceAgeSeconds) * time.Second,
		Timeout:     timeout,
		Concurrency: cfg.RoadMatchConcurrency,
	}, log)
}
