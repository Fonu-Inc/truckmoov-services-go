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
	"github.com/truckmoov/services-go/pkg/identity"
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
	serviceCaller := cfg.ServiceCaller()
	userTokens, err := cfg.UserTokenVerifier()
	if err != nil {
		log.Error("user_token_keys_invalid", "error", err.Error())
		os.Exit(1)
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
	dotgoCallbackURL := infrastructure.DotgoCallbackURLWithToken(cfg.DotgoCallbackURL, cfg.DotgoWebhookSecret)
	if strings.TrimSpace(cfg.DotgoCallbackURL) != "" && dotgoCallbackURL == "" {
		log.Warn("sms_callback_unusable", "message", "delivery receipts are not requested: DOTGO_CALLBACK_URL must be an absolute http(s) URL of this service's /notify/sms/webhook route, and DOTGO_WEBHOOK_SECRET must be set")
	}
	smsMessageProvider := infrastructure.NewDotgoSMSMessageProvider(
		cfg.DotgoBaseURL,
		cfg.DotgoAccountID,
		cfg.DotgoAPIToken,
		cfg.DotgoSenderMask,
		dotgoCallbackURL,
	)
	emailMessageProvider := infrastructure.NewSMTPEmailMessageProvider(
		cfg.SMTPHost,
		cfg.SMTPPort,
		cfg.SMTPUsername,
		cfg.SMTPPassword,
		cfg.SMTPFromAddress,
		cfg.SMTPFromName,
	)
	if strings.TrimSpace(cfg.SMTPHost) == "" || strings.TrimSpace(cfg.SMTPFromAddress) == "" {
		log.Warn("email_provider_unconfigured", "message", "set SMTP_HOST, SMTP_PORT, and SMTP_FROM_ADDRESS before enabling email delivery")
	}
	messageProvider := infrastructure.NewMultiChannelMessageProvider(whatsAppMessageProvider, smsMessageProvider, emailMessageProvider)
	templateProvider := infrastructure.NewMetaWhatsAppTemplateProvider(
		cfg.WhatsAppAPIBaseURL,
		whatsAppAccountRepository,
		tokenCipher,
		cfg.WhatsAppBusinessAccountID,
		cfg.WhatsAppAccessToken,
	)
	if cfg.DotgoBaseURL == "" || cfg.DotgoAccountID == "" || cfg.DotgoAPIToken == "" || cfg.DotgoSenderMask == "" {
		log.Warn("sms_provider_unconfigured", "message", "set DOTGO_BASE_URL, DOTGO_TRANSACTIONAL_ACCOUNT_ID, DOTGO_TRANSACTIONAL_API_TOKEN, and DOTGO_TRANSACTIONAL_SENDER_MASK before enabling SMS fallback delivery")
	}
	deliveryPublisher := infrastructure.NewDeliveryEventPublisher(bus)
	deliveryLogRepository := infrastructure.NewPostgresDeliveryLogRepository(postgresDB)
	if err := deliveryLogRepository.EnsureSchema(ctx); err != nil {
		log.Error("delivery_log_schema_failed", "error", err)
	}
	clock := infrastructure.SystemClock{}
	idGenerator := infrastructure.TimeIDGenerator{}

	usageCharger := infrastructure.NewAuthUsageCharger(cfg.AuthServiceURL, serviceCaller, nil)
	alertSettingsReader := infrastructure.NewAuthAlertSettingsReader(cfg.AuthServiceURL, serviceCaller, nil)

	sendMessageUseCase := application.NewSendMessageUseCase(
		outboxRepository,
		messageProvider,
		deliveryPublisher,
		deliveryLogRepository,
		usageCharger,
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
	driverStatusRequestSender := infrastructure.NewDispatchDriverStatusRequestSender(cfg.DispatchServiceURL, serviceCaller, nil)
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

	if cfg.EventSigningSecret == "" {
		log.Error("event_signing_secret_unset", "message", "set EVENT_SIGNING_SECRET; every notification.send event will be rejected until then")
	}
	consumeNotificationUseCase := application.NewConsumeNotificationUseCase(sendMessageUseCase, cfg.EventSigningSecret, log)
	consumeJobOfferNotificationUseCase := application.NewConsumeJobOfferNotificationUseCase(sendMessageUseCase, log)
	dispatchAlertUseCase := application.NewDispatchAlertUseCase(alertSettingsReader, sendMessageUseCase, log)
	// Platform alerts ride the system tenant, so they are a separate subject
	// from a tenant's own notifications and a separate consumer.
	if _, alertSubErr := bus.Subscribe(ctx, "tm.system.alert.raised", "platform-alerts", dispatchAlertUseCase.Handle); alertSubErr != nil {
		log.Error("platform_alert_subscribe_failed", "error", alertSubErr)
	}

	if _, subErr := bus.Subscribe(ctx, "tm.*.notification.send", "notification-send", consumeNotificationUseCase.Handle); subErr != nil {
		log.Warn("notification_subscribe_failed", "error", subErr.Error())
	}
	if _, subErr := bus.Subscribe(ctx, "tm.*.job.offer.created", "job-offer-created-notifications", consumeJobOfferNotificationUseCase.Handle); subErr != nil {
		log.Error("job_offer_created_subscribe_failed", "error", subErr.Error())
	}
	if _, subErr := bus.Subscribe(ctx, "tm.*.job.offer.responded", "job-offer-responded-notifications", consumeJobOfferNotificationUseCase.Handle); subErr != nil {
		log.Error("job_offer_responded_subscribe_failed", "error", subErr.Error())
	}

	listDeliveryLogUseCase := application.NewListDeliveryLogUseCase(deliveryLogRepository)

	campaignRepository := infrastructure.NewPostgresCampaignRepository(postgresDB)
	if err := campaignRepository.EnsureSchema(ctx); err != nil {
		log.Error("campaign_schema_failed", "error", err)
	}
	suppressionRepository := infrastructure.NewPostgresSuppressionRepository(postgresDB)
	if err := suppressionRepository.EnsureSchema(ctx); err != nil {
		log.Error("campaign_suppression_schema_failed", "error", err)
	}

	createCampaignUseCase := application.NewCreateCampaignUseCase(
		campaignRepository,
		suppressionRepository,
		clock,
		idGenerator,
	)
	sendCampaignUseCase := application.NewSendCampaignUseCase(
		campaignRepository,
		suppressionRepository,
		messageProvider,
		// WhatsApp goes out as a template, which is the only path that returns
		// a provider message id for later receipts to match on.
		whatsAppMessageProvider,
		cfg.WhatsAppTemplateLanguage,
		usageCharger,
		clock,
	)
	listCampaignsUseCase := application.NewListCampaignsUseCase(campaignRepository)
	suppressRecipientUseCase := application.NewSuppressRecipientUseCase(suppressionRepository)

	// Delivery and read receipts already arrive on the WhatsApp webhook, so
	// campaign recipients are updated from the same place rather than a second
	// stream of the same events.
	handleWhatsAppWebhookUseCase = handleWhatsAppWebhookUseCase.WithCampaignReceipts(
		application.NewApplyCampaignReceiptUseCase(campaignRepository, clock),
	)

	// An inbound photo that is not a driver-status reply becomes a waybill in
	// fleet. Reuses the same provider that already downloads/sends WhatsApp
	// media, and the same internal-service-token pattern dispatch uses to call
	// fleet.
	fleetWaybillClient := infrastructure.NewFleetWaybillClient(cfg.FleetServiceURL, serviceCaller, nil)
	handleWhatsAppWebhookUseCase = handleWhatsAppWebhookUseCase.WithWaybillCapture(
		whatsAppMessageProvider,
		fleetWaybillClient,
	)

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
		listDeliveryLogUseCase,
		activityPublisher,
		clock,
		cfg.WhatsAppWebhookVerifyToken,
		cfg.WhatsAppAppSecret,
	)
	handler = handler.WithSMSReceipts(
		application.NewHandleSMSDeliveryReceiptUseCase(outboxRepository, deliveryLogRepository, deliveryPublisher, clock, idGenerator),
		cfg.DotgoWebhookSecret,
	)
	handler = handler.WithCampaigns(
		campaignRepository,
		createCampaignUseCase,
		sendCampaignUseCase,
		listCampaignsUseCase,
		suppressRecipientUseCase,
	)

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
	routes := httpx.NewRoutes(mux, cfg.PortalEnforced, log, httpx.RefusalPublisher(bus, "notification")).EnforcePermissions(cfg.PermissionsEnforced)
	handler.RegisterRoutes(routes)

	metrics := httpx.NewMetrics(cfg.ServiceName)
	routes.Handle("/metrics", []httpx.Audience{httpx.Public}, httpx.None(), metrics.Handler())

	readiness := httpx.ReadinessHandler("notification",
		httpx.ReadinessCheck{Name: "postgres", Probe: postgresDB.PingContext},
		httpx.ReadinessCheck{Name: "nats", Probe: bus.Ping},
		httpx.ReadinessCheck{Name: "redis", Probe: func(context.Context) error { return redisClient.Ping() }},
	)
	routes.Handle("/ready", []httpx.Audience{httpx.Public}, httpx.None(), readiness)
	routes.Handle("/notify/ready", []httpx.Audience{httpx.Public}, httpx.None(), readiness)

	handlerWithAuth := httpx.WithAuthContext(httpx.WithServiceCallers(httpx.WithCurrentAccess(metrics.Middleware(mux), accessClient), cfg.ServiceTokenVerifier(), httpx.ServiceRoles{
		"auth":     "DISPATCHER",
		"fleet":    "DISPATCHER",
		"dispatch": "DISPATCHER",
	}), userTokens)
	handlerWithAuth = httpx.WithErrorAlerts(handlerWithAuth, alert.NewNtfyClient(cfg.NtfyErrorTopicURL, cfg.NtfyErrorAuthToken), httpx.NewRecentRequests(3), cfg.ServiceName)
	// Kong answers CORS in every deployed environment.
	var handlerWithCORS http.Handler = handlerWithAuth
	if cfg.CORSEnabled {
		handlerWithCORS = httpx.WithCORS(handlerWithCORS, cfg.CORSOrigins)
	}

	if runErr := httpx.Run(ctx, ":"+cfg.Port, httpx.WithLogging(handlerWithCORS, log), log); runErr != nil {
		log.Error("service_stopped", "error", runErr.Error())
		os.Exit(1)
	}
}
