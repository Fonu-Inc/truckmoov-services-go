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

	handler := dispatchhttp.NewHandler(
		publishJobStatusUseCase,
		jobLifecycleUseCase,
		routingUseCase,
		sendJobInstructionUseCase,
		activitylog.NewPublisher(bus),
		fleetClient,
	)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	handlerWithCORS := httpx.WithCORS(mux, cfg.CORSOrigins)
	handlerWithCORS = httpx.WithRateLimit(handlerWithCORS, cfg.RateLimitRPS, cfg.RateLimitBurst)
	handlerWithCORS = httpx.WithAuthContext(handlerWithCORS, cfg.JWTSecret)
	handlerWithCORS = httpx.WithErrorAlerts(handlerWithCORS, alert.NewNtfyClient(cfg.NtfyErrorTopicURL, cfg.NtfyErrorAuthToken), httpx.NewRecentRequests(3), cfg.ServiceName)

	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
