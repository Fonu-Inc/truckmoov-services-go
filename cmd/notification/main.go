package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	notificationhttp "github.com/truckmoov/services-go/internal/notification/interfaces/http"
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

	cfg := config.Load("NOTIFICATION", "3003")
	log := logger.New("notification")
	log.Info("jwt_key_id", "key_id", cfg.JWTKeyID)

	if err := cfg.Validate(); err != nil {
		log.Error("config_invalid", "error", err.Error())
		os.Exit(1)
	}
	if cfg.UsingDefaultJWTSecret() {
		log.Warn("jwt_secret_default", "detail", "JWT_SECRET is unset or the default placeholder; set it before running in production")
	}

	shutdownTelemetry, err := otel.Setup(ctx, "notification", log)
	if err != nil {
		log.Error("otel_setup_failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		_ = shutdownTelemetry(context.Background())
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
	outboxRepository := infrastructure.NewRedisOutboxRepository(redisClient)
	conversationRepository := infrastructure.NewPostgresWhatsAppConversationRepository(postgresDB)
	if err := conversationRepository.EnsureSchema(ctx); err != nil {
		log.Error("conversation_schema_failed", "error", err.Error())
		os.Exit(1)
	}
	whatsAppAccountRepository := infrastructure.NewPostgresWhatsAppAccountRepository(postgresDB)
	if err := whatsAppAccountRepository.EnsureSchema(ctx); err != nil {
		log.Error("whatsapp_account_schema_failed", "error", err.Error())
		os.Exit(1)
	}

	var tokenCipher *infrastructure.AESTokenCipher
	if strings.TrimSpace(cfg.WhatsAppAccountEncryptionKey) != "" {
		tokenCipher, err = infrastructure.NewAESTokenCipher(cfg.WhatsAppAccountEncryptionKey)
		if err != nil {
			log.Error("whatsapp_account_cipher_invalid", "error", err.Error())
			os.Exit(1)
		}
	} else {
		log.Warn("whatsapp_account_cipher_unconfigured", "message", "set WHATSAPP_ACCOUNT_ENCRYPTION_KEY to enable connected WhatsApp accounts")
	}

	embeddedSignupProvider := infrastructure.NewMetaWhatsAppEmbeddedSignupProvider(
		cfg.WhatsAppAPIBaseURL,
		cfg.WhatsAppAppID,
		cfg.WhatsAppAppSecret,
	)
	if strings.TrimSpace(cfg.WhatsAppAppID) == "" || strings.TrimSpace(cfg.WhatsAppAppSecret) == "" {
		log.Warn("whatsapp_embedded_signup_unconfigured", "message", "set WHATSAPP_APP_ID and WHATSAPP_APP_SECRET to enable embedded signup")
	}

	whatsAppMessageProvider := infrastructure.NewMetaWhatsAppMessageProvider(
		cfg.WhatsAppAPIBaseURL,
		whatsAppAccountRepository,
		tokenCipher,
		cfg.WhatsAppPhoneNumberID,
		cfg.WhatsAppAccessToken,
	)
	smsMessageProvider := infrastructure.NewDotgoSMSMessageProvider(
		cfg.SMSServiceURL,
		cfg.SMSServiceUsername,
		cfg.SMSServicePassword,
		cfg.SMSServiceSenderID,
	)
	messageProvider := infrastructure.NewMultiChannelMessageProvider(whatsAppMessageProvider, smsMessageProvider)
	templateProvider := infrastructure.NewMetaWhatsAppTemplateProvider(
		cfg.WhatsAppAPIBaseURL,
		whatsAppAccountRepository,
		tokenCipher,
		cfg.WhatsAppBusinessAccountID,
		cfg.WhatsAppAccessToken,
	)
	if cfg.SMSServiceURL == "" || cfg.SMSServiceUsername == "" || cfg.SMSServicePassword == "" || cfg.SMSServiceSenderID == "" {
		log.Warn("sms_provider_unconfigured", "message", "set SMS_SERVICE_URL, SMS_SERVICE_USER_NAME, SMS_SERVICE_PASSWORD, and SMS_SERVICE_SENDER_ID before enabling SMS fallback delivery")
	}
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
	connectWhatsAppAccountUseCase := application.NewConnectWhatsAppAccountUseCase(
		whatsAppAccountRepository,
		embeddedSignupProvider,
		tokenCipher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	getWhatsAppAccountUseCase := application.NewGetWhatsAppAccountUseCase(whatsAppAccountRepository, cfg.DefaultTenant)
	disconnectWhatsAppAccountUseCase := application.NewDisconnectWhatsAppAccountUseCase(whatsAppAccountRepository, cfg.DefaultTenant)
	createWhatsAppTemplateUseCase := application.NewCreateWhatsAppTemplateUseCase(templateProvider)
	listWhatsAppTemplatesUseCase := application.NewListWhatsAppTemplatesUseCase(templateProvider)
	sendOtherTemplateUseCase := application.NewSendOtherTemplateUseCase(
		outboxRepository,
		conversationRepository,
		templateProvider,
		whatsAppMessageProvider,
		deliveryPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	sendJobInstructionUseCase := application.NewSendJobInstructionUseCase(
		outboxRepository,
		conversationRepository,
		templateProvider,
		whatsAppMessageProvider,
		deliveryPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	sendWhatsAppTextMessageUseCase := application.NewSendWhatsAppTextMessageUseCase(
		outboxRepository,
		conversationRepository,
		whatsAppMessageProvider,
		deliveryPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	sendWhatsAppMediaMessageUseCase := application.NewSendWhatsAppMediaMessageUseCase(
		outboxRepository,
		conversationRepository,
		whatsAppMessageProvider,
		deliveryPublisher,
		clock,
		idGenerator,
		cfg.DefaultTenant,
	)
	listWhatsAppConversationsUseCase := application.NewListWhatsAppConversationsUseCase(conversationRepository, clock, cfg.DefaultTenant)
	getWhatsAppConversationUseCase := application.NewGetWhatsAppConversationUseCase(conversationRepository, cfg.DefaultTenant)
	listWhatsAppConversationMessagesUseCase := application.NewListWhatsAppConversationMessagesUseCase(conversationRepository, cfg.DefaultTenant)
	driverStatusRequestSender := infrastructure.NewDispatchDriverStatusRequestSender(cfg.DispatchServiceURL, cfg.JWTSecret, nil)
	handleWhatsAppWebhookUseCase := application.NewHandleWhatsAppWebhookUseCase(
		conversationRepository,
		outboxRepository,
		whatsAppAccountRepository,
		messageProvider,
		driverStatusRequestSender,
		deliveryPublisher,
		activityPublisher,
		clock,
		idGenerator,
	)

	consumeNotificationUseCase := application.NewConsumeNotificationUseCase(sendMessageUseCase, log)
	if _, subErr := bus.Subscribe(ctx, "tm.*.notification.send", "notification-send", consumeNotificationUseCase.Handle); subErr != nil {
		log.Warn("notification_subscribe_failed", "error", subErr.Error())
	}

	handler := notificationhttp.NewHandler(
		sendMessageUseCase,
		retryMessageUseCase,
		connectWhatsAppAccountUseCase,
		getWhatsAppAccountUseCase,
		disconnectWhatsAppAccountUseCase,
		createWhatsAppTemplateUseCase,
		listWhatsAppTemplatesUseCase,
		sendOtherTemplateUseCase,
		sendJobInstructionUseCase,
		sendWhatsAppTextMessageUseCase,
		sendWhatsAppMediaMessageUseCase,
		listWhatsAppConversationsUseCase,
		getWhatsAppConversationUseCase,
		listWhatsAppConversationMessagesUseCase,
		handleWhatsAppWebhookUseCase,
		activityPublisher,
		clock,
		cfg.WhatsAppWebhookVerifyToken,
	)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	metrics := httpx.NewMetrics(cfg.ServiceName)
	mux.Handle("/metrics", metrics.Handler())

	readiness := httpx.ReadinessHandler("notification",
		httpx.ReadinessCheck{Name: "postgres", Probe: postgresDB.PingContext},
		httpx.ReadinessCheck{Name: "nats", Probe: bus.Ping},
		httpx.ReadinessCheck{Name: "redis", Probe: func(context.Context) error { return redisClient.Ping() }},
	)
	mux.Handle("/ready", readiness)
	mux.Handle("/notify/ready", readiness)

	handlerWithAuth := httpx.WithAuthContext(metrics.Middleware(mux), cfg.JWTSecret)
	handlerWithAuth = httpx.WithErrorAlerts(handlerWithAuth, alert.NewNtfyClient(cfg.NtfyErrorTopicURL, cfg.NtfyErrorAuthToken), httpx.NewRecentRequests(3), cfg.ServiceName)
	handlerWithCORS := httpx.WithCORS(handlerWithAuth, cfg.CORSOrigins)

	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
