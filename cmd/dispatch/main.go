package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	solverv1 "github.com/truckmoov/services-go/api/proto/gen/proto/solver/v1"
	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/infrastructure"
	dispatchhttp "github.com/truckmoov/services-go/internal/dispatch/interfaces/http"
	"github.com/truckmoov/services-go/pkg/activitylog"
	"github.com/truckmoov/services-go/pkg/alert"
	"github.com/truckmoov/services-go/pkg/config"
	"github.com/truckmoov/services-go/pkg/eventbus"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/logger"
	"github.com/truckmoov/services-go/pkg/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load("DISPATCH", "3005")
	log := logger.New("dispatch")
	log.Info("jwt_key_id", "key_id", cfg.JWTKeyID)

	if err := cfg.Validate(); err != nil {
		log.Error("config_invalid", "error", err.Error())
		os.Exit(1)
	}
	if cfg.UsingDefaultJWTSecret() {
		log.Warn("jwt_secret_default", "detail", "JWT_SECRET is unset or the default placeholder; set it before running in production")
	}

	shutdownTelemetry, err := otel.Setup(ctx, "dispatch", log)
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

	postgresDB, err := sql.Open("pgx", cfg.PostgresDSN())
	if err != nil {
		log.Error("postgres_open_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		_ = postgresDB.Close()
	}()
	if err := postgresDB.PingContext(ctx); err != nil {
		log.Error("postgres_ping_failed", "error", err.Error())
		os.Exit(1)
	}

	jobStatusPublisher := infrastructure.NewJobStatusEventPublisher(bus)
	publishJobStatusUseCase := application.NewPublishJobStatusChangedUseCase(
		jobStatusPublisher,
		infrastructure.SystemClock{},
		infrastructure.TimeIDGenerator{},
		cfg.DefaultTenant,
	)
	jobRepository := infrastructure.NewPostgresJobRepository(postgresDB)
	if err := jobRepository.EnsureSchema(ctx); err != nil {
		log.Error("dispatch_schema_init_failed", "error", err.Error())
		os.Exit(1)
	}
	driverInstructionNotifier := infrastructure.NewDriverInstructionNotifier(bus)
	jobLifecycleUseCase := application.NewJobLifecycleUseCase(
		jobRepository,
		publishJobStatusUseCase,
		driverInstructionNotifier,
		infrastructure.SystemClock{},
		infrastructure.TimeIDGenerator{},
		cfg.DefaultTenant,
	)

	var routeOptimizer application.RouteOptimizer
	if cfg.RouteSolverAddr != "" {
		conn, dialErr := grpc.DialContext(
			ctx,
			cfg.RouteSolverAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if dialErr != nil {
			log.Error("route_solver_dial_failed", "error", dialErr.Error(), "address", cfg.RouteSolverAddr)
			os.Exit(1)
		}
		defer func() {
			_ = conn.Close()
		}()

		routeOptimizer = infrastructure.NewSolverRouteOptimizer(
			solverv1.NewRouteSolverClient(conn),
			infrastructure.NewValhallaClient(cfg.ValhallaURL),
			cfg.RouteSolverAuthToken,
		)
		log.Info("route_solver_enabled", "address", cfg.RouteSolverAddr, "valhalla_url", cfg.ValhallaURL)
	} else {
		log.Warn("route_solver_disabled", "detail", "ROUTE_SOLVER_ADDR is empty; route optimization falls back to a straight-line planner with no turn-by-turn geometry or alternate routes")
	}

	routingUseCase := application.NewRoutingUseCase(
		jobRepository,
		infrastructure.SystemClock{},
		cfg.DefaultTenant,
		routeOptimizer,
	)
	jobInstructionSender := infrastructure.NewNotificationJobInstructionSender(cfg.NotificationServiceURL, cfg.JWTSecret, nil)
	sendJobInstructionUseCase := application.NewSendJobInstructionUseCase(
		jobRepository,
		jobInstructionSender,
		infrastructure.SystemClock{},
		infrastructure.TimeIDGenerator{},
		cfg.DefaultTenant,
	)

	alertPublisher := infrastructure.NewAlertEventPublisher(bus)
	etaMonitor := application.NewEtaMonitorUseCase(
		jobRepository,
		alertPublisher,
		infrastructure.SystemClock{},
		cfg.DefaultTenant,
	)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				etaMonitor.Check(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	fleetClient := infrastructure.NewFleetClient(cfg.FleetServiceURL, cfg.JWTSecret, nil)

	jobOfferRepository := infrastructure.NewPostgresJobOfferRepository(postgresDB)
	if err := jobOfferRepository.EnsureSchema(ctx); err != nil {
		log.Error("job_offer_schema_failed", "error", err)
	}
	entitlementsReader := infrastructure.NewAuthEntitlementsReader(cfg.AuthServiceURL, cfg.JWTSecret, nil)
	jobOfferPublisher := infrastructure.NewJobOfferEventPublisher(bus)
	activeJobCounter := func(_ context.Context, tenantID string) (int, error) {
		count := 0
		for _, job := range jobRepository.FindByTenant(tenantID) {
			if job.IsActive() {
				count++
			}
		}
		return count, nil
	}

	createJobOfferUseCase := application.NewCreateJobOfferUseCase(
		jobOfferRepository, jobRepository, jobOfferPublisher,
		infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{},
	)
	acceptJobOfferUseCase := application.NewAcceptJobOfferUseCase(
		jobOfferRepository, entitlementsReader, activeJobCounter, jobOfferPublisher, infrastructure.SystemClock{},
	)
	declineJobOfferUseCase := application.NewDeclineJobOfferUseCase(
		jobOfferRepository, jobOfferPublisher, infrastructure.SystemClock{},
	)
	listJobOffersUseCase := application.NewListJobOffersUseCase(jobOfferRepository)
	activeJobCapacityUseCase := application.NewActiveJobCapacityUseCase(entitlementsReader, activeJobCounter)

	expireJobOffersUseCase := application.NewExpireJobOffersUseCase(jobOfferRepository, infrastructure.SystemClock{})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if expired, err := expireJobOffersUseCase.Execute(ctx); err != nil {
					log.Error("job_offer_expiry_failed", "error", err)
				} else if expired > 0 {
					log.Warn("job_offers_expired", "count", expired)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	aggregatorRelationshipRepository := infrastructure.NewPostgresAggregatorRelationshipRepository(postgresDB)
	if err := aggregatorRelationshipRepository.EnsureSchema(ctx); err != nil {
		log.Error("aggregator_relationship_schema_failed", "error", err)
	}
	operatorRatingRepository := infrastructure.NewPostgresOperatorRatingRepository(postgresDB)
	if err := operatorRatingRepository.EnsureSchema(ctx); err != nil {
		log.Error("operator_rating_schema_failed", "error", err)
	}
	settlementRepository := infrastructure.NewPostgresSettlementRepository(postgresDB)
	if err := settlementRepository.EnsureSchema(ctx); err != nil {
		log.Error("settlement_schema_failed", "error", err)
	}
	aggregatorActivityRepository := infrastructure.NewPostgresAggregatorActivityRepository(postgresDB)
	if err := aggregatorActivityRepository.EnsureSchema(ctx); err != nil {
		log.Error("aggregator_activity_schema_failed", "error", err)
	}
	fleetCountsReader := infrastructure.NewFleetCountsReader(cfg.FleetServiceURL, cfg.JWTSecret, nil)
	complianceStatusReader := infrastructure.NewComplianceStatusReader(cfg.FleetServiceURL, cfg.JWTSecret, nil)

	summaryFiguresUseCase := application.NewSummaryFiguresUseCase(aggregatorRelationshipRepository, jobRepository, fleetCountsReader, operatorRatingRepository)
	listLinkedOperatorsUseCase := application.NewListLinkedOperatorsUseCase(aggregatorRelationshipRepository, jobRepository, fleetCountsReader, operatorRatingRepository)
	addOperatorUseCase := application.NewAddOperatorUseCase(aggregatorRelationshipRepository, aggregatorActivityRepository, infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{})
	removeOperatorUseCase := application.NewRemoveOperatorUseCase(aggregatorRelationshipRepository, jobOfferRepository, jobRepository, infrastructure.SystemClock{})
	acceptAggregatorInviteUseCase := application.NewAcceptAggregatorInviteUseCase(aggregatorRelationshipRepository, aggregatorActivityRepository, infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{})
	subDispatchQueueUseCase := application.NewSubDispatchQueueUseCase(jobOfferRepository, jobRepository)
	assignSubDispatchJobUseCase := application.NewAssignSubDispatchJobUseCase(aggregatorRelationshipRepository, createJobOfferUseCase, aggregatorActivityRepository, infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{})
	operatorPerformanceUseCase := application.NewOperatorPerformanceUseCase(aggregatorRelationshipRepository, jobOfferRepository, jobRepository, operatorRatingRepository, infrastructure.SystemClock{})
	topPerformingOperatorsUseCase := application.NewTopPerformingOperatorsUseCase(aggregatorRelationshipRepository, jobOfferRepository, jobRepository, operatorRatingRepository)
	rateOperatorUseCase := application.NewRateOperatorUseCase(jobOfferRepository, jobRepository, operatorRatingRepository, infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{})
	listSettlementsUseCase := application.NewListSettlementsUseCase(settlementRepository)
	paySettlementUseCase := application.NewPaySettlementUseCase(settlementRepository, aggregatorActivityRepository, infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{})
	listAggregatorActivityUseCase := application.NewListAggregatorActivityUseCase(aggregatorActivityRepository)
	complianceSnapshotUseCase := application.NewComplianceSnapshotUseCase(aggregatorRelationshipRepository, complianceStatusReader)

	weeklySettlementCloseUseCase := application.NewWeeklySettlementCloseUseCase(
		aggregatorRelationshipRepository, jobOfferRepository, jobRepository, settlementRepository,
		infrastructure.SystemClock{}, infrastructure.TimeIDGenerator{},
	)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if closed, err := weeklySettlementCloseUseCase.Execute(ctx); err != nil {
					log.Error("settlement_close_failed", "error", err)
				} else if closed > 0 {
					log.Info("settlements_closed", "count", closed)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	handler := dispatchhttp.NewHandler(
		publishJobStatusUseCase,
		jobLifecycleUseCase,
		routingUseCase,
		sendJobInstructionUseCase,
		activitylog.NewPublisher(bus),
		fleetClient,
	)
	handler = handler.WithJobOffers(
		createJobOfferUseCase,
		acceptJobOfferUseCase,
		declineJobOfferUseCase,
		listJobOffersUseCase,
		activeJobCapacityUseCase,
	)
	handler = handler.WithAggregator(
		summaryFiguresUseCase,
		listLinkedOperatorsUseCase,
		addOperatorUseCase,
		removeOperatorUseCase,
		acceptAggregatorInviteUseCase,
		subDispatchQueueUseCase,
		assignSubDispatchJobUseCase,
		operatorPerformanceUseCase,
		topPerformingOperatorsUseCase,
		rateOperatorUseCase,
		listSettlementsUseCase,
		paySettlementUseCase,
		listAggregatorActivityUseCase,
		complianceSnapshotUseCase,
	)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	metrics := httpx.NewMetrics(cfg.ServiceName)
	mux.Handle("/metrics", metrics.Handler())

	readiness := httpx.ReadinessHandler("dispatch",
		httpx.ReadinessCheck{Name: "postgres", Probe: postgresDB.PingContext},
		httpx.ReadinessCheck{Name: "nats", Probe: bus.Ping},
	)
	mux.Handle("/ready", readiness)
	mux.Handle("/dispatch/ready", readiness)

	// Kong answers CORS in every deployed environment. SERVICE_CORS_ENABLED=true
	// turns it on here instead, which a local run without Kong needs.
	var handlerWithCORS http.Handler = metrics.Middleware(mux)
	if cfg.CORSEnabled {
		handlerWithCORS = httpx.WithCORS(handlerWithCORS, cfg.CORSOrigins)
	}
	handlerWithCORS = httpx.WithRateLimit(handlerWithCORS, cfg.RateLimitRPS, cfg.RateLimitBurst)
	handlerWithCORS = httpx.WithAuthContext(handlerWithCORS, cfg.JWTSecret)
	handlerWithCORS = httpx.WithErrorAlerts(handlerWithCORS, alert.NewNtfyClient(cfg.NtfyErrorTopicURL, cfg.NtfyErrorAuthToken), httpx.NewRecentRequests(3), cfg.ServiceName)

	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
