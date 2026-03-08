package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/infrastructure"
	trackinghttp "github.com/truckmoov/services-go/internal/tracking/interfaces/http"
	"github.com/truckmoov/services-go/pkg/config"
	"github.com/truckmoov/services-go/pkg/eventbus"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/logger"
	"github.com/truckmoov/services-go/pkg/otel"
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

	bus := eventbus.New(ctx, cfg.NATSURL, cfg.NATSStream, log)
	defer func() {
		_ = bus.Close()
	}()

	eventPublisher := infrastructure.NewEventPublisher(bus)
	clock := infrastructure.SystemClock{}
	idGenerator := infrastructure.TimeIDGenerator{}

	ingestLocationUseCase := application.NewIngestLocationUseCase(
		eventPublisher,
		infrastructure.NewInMemoryLatestLocationRepository(),
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	publishAlertUseCase := application.NewPublishAlertUseCase(
		eventPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)

	handler := trackinghttp.NewHandler(ingestLocationUseCase, publishAlertUseCase)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	handlerWithCORS := httpx.WithCORS(mux, cfg.CORSOrigins)

	// cmd/tracking is kept as composition root only: all business rules live in internal/tracking.
	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
