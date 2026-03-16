package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	notificationhttp "github.com/truckmoov/services-go/internal/notification/interfaces/http"
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

	cfg := config.Load("NOTIFICATION", "3003")
	log := logger.New("notification")

	shutdownTelemetry, err := otel.Setup(ctx, "notification", log)
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

	redisClient := redisstore.NewStore(cfg.RedisAddr, cfg.RedisDB)
	outboxRepository := infrastructure.NewRedisOutboxRepository(redisClient)
	messageProvider := infrastructure.NewMTNMessageProvider()
	deliveryPublisher := infrastructure.NewDeliveryEventPublisher(bus)
	clock := infrastructure.SystemClock{}
	idGenerator := infrastructure.TimeIDGenerator{}

	sendMessageUseCase := application.NewSendMessageUseCase(
		outboxRepository,
		messageProvider,
		deliveryPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	retryMessageUseCase := application.NewRetryMessageUseCase(
		outboxRepository,
		messageProvider,
		deliveryPublisher,
		clock,
		idGenerator,
	)

	consumeNotificationUseCase := application.NewConsumeNotificationUseCase(sendMessageUseCase, log)
	if _, subErr := bus.Subscribe(ctx, "tm.*.notification.send", "notification-send", consumeNotificationUseCase.Handle); subErr != nil {
		log.Warn("notification_subscribe_failed", "error", subErr.Error())
	}

	handler := notificationhttp.NewHandler(sendMessageUseCase, retryMessageUseCase)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	handlerWithCORS := httpx.WithCORS(mux, cfg.CORSOrigins)

	// cmd/notification wires adapters and use-cases; domain/application logic stays under internal/notification.
	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
