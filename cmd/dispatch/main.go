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

	shutdownTelemetry, err := otel.Setup(ctx, "dispatch", log)
	if err != nil {
		log.Error("otel_setup_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		_ = shutdownTelemetry(context.Background())
	}()

	bus := eventbus.New(ctx, cfg.NATSURL, cfg.NATSStream, log)
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

	handler := dispatchhttp.NewHandler(
		publishJobStatusUseCase,
		jobLifecycleUseCase,
		routingUseCase,
		sendJobInstructionUseCase,
	)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	handlerWithCORS := httpx.WithCORS(mux, cfg.CORSOrigins)
	handlerWithCORS = httpx.WithRateLimit(handlerWithCORS, cfg.RateLimitRPS, cfg.RateLimitBurst)

	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
