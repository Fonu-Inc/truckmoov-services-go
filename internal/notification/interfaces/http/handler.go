package notificationhttp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/activitylog"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type activityPublisher interface {
	Publish(ctx context.Context, command activitylog.PublishCommand) error
}

type sendRequest struct {
	TenantID  string `json:"tenant_id"`
	Channel   string `json:"channel"`
	Recipient string `json:"recipient"`
	Content   string `json:"content"`
}

type createWhatsAppTemplateRequest struct {
	Name                   string           `json:"name"`
	TemplateType           string           `json:"template_type,omitempty"`
	TemplateTypeAlt        string           `json:"templateType,omitempty"`
	Header                 string           `json:"header,omitempty"`
	Body                   string           `json:"body,omitempty"`
	Language               string           `json:"language,omitempty"`
	Category               string           `json:"category,omitempty"`
	AllowCategoryChange    *bool            `json:"allow_category_change,omitempty"`
	AllowCategoryChangeAlt *bool            `json:"allowCategoryChange,omitempty"`
	ParameterFormat        string           `json:"parameter_format,omitempty"`
	ParameterFormatAlt     string           `json:"parameterFormat,omitempty"`
	Components             []map[string]any `json:"components,omitempty"`
}

type sendJobInstructionHeaderMediaRequest struct {
	Type     string `json:"type,omitempty"`
	Link     string `json:"link,omitempty"`
	ID       string `json:"id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type sendJobInstructionButtonParameterRequest struct {
	Index   int    `json:"index"`
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Payload string `json:"payload,omitempty"`
}

type sendOtherTemplateRequest struct {
	To                   string                                     `json:"to,omitempty"`
	Recipient            string                                     `json:"recipient,omitempty"`
	TemplateName         string                                     `json:"template_name,omitempty"`
	TemplateNameAlt      string                                     `json:"templateName,omitempty"`
	Language             string                                     `json:"language,omitempty"`
	Variables            map[string]string                          `json:"variables,omitempty"`
	TemplateVariables    map[string]string                          `json:"template_variables,omitempty"`
	TemplateVariablesAlt map[string]string                          `json:"templateVariables,omitempty"`
	HeaderMedia          *sendJobInstructionHeaderMediaRequest      `json:"header_media,omitempty"`
	HeaderMediaAlt       *sendJobInstructionHeaderMediaRequest      `json:"headerMedia,omitempty"`
	ButtonParameters     []sendJobInstructionButtonParameterRequest `json:"button_parameters,omitempty"`
	ButtonParametersAlt  []sendJobInstructionButtonParameterRequest `json:"buttonParameters,omitempty"`
}

type sendJobInstructionRequest struct {
	JobID               string                                     `json:"job_id,omitempty"`
	JobIDAlt            string                                     `json:"jobId,omitempty"`
	DriverID            string                                     `json:"driver_id,omitempty"`
	DriverIDAlt         string                                     `json:"driverId,omitempty"`
	To                  string                                     `json:"to,omitempty"`
	Recipient           string                                     `json:"recipient,omitempty"`
	TemplateName        string                                     `json:"template_name,omitempty"`
	TemplateNameAlt     string                                     `json:"templateName,omitempty"`
	Language            string                                     `json:"language,omitempty"`
	DriverName          string                                     `json:"driver_name,omitempty"`
	DriverNameAlt       string                                     `json:"driverName,omitempty"`
	JobReference        string                                     `json:"job_reference,omitempty"`
	JobReferenceAlt     string                                     `json:"jobReference,omitempty"`
	PickupLocation      string                                     `json:"pickup_location,omitempty"`
	PickupLocationAlt   string                                     `json:"pickupLocation,omitempty"`
	DeliveryLocation    string                                     `json:"delivery_location,omitempty"`
	DeliveryLocationAlt string                                     `json:"deliveryLocation,omitempty"`
	CargoDescription    string                                     `json:"cargo_description,omitempty"`
	CargoDescriptionAlt string                                     `json:"cargoDescription,omitempty"`
	Priority            string                                     `json:"priority,omitempty"`
	HeaderMedia         *sendJobInstructionHeaderMediaRequest      `json:"header_media,omitempty"`
	HeaderMediaAlt      *sendJobInstructionHeaderMediaRequest      `json:"headerMedia,omitempty"`
	ButtonParameters    []sendJobInstructionButtonParameterRequest `json:"button_parameters,omitempty"`
	ButtonParametersAlt []sendJobInstructionButtonParameterRequest `json:"buttonParameters,omitempty"`
}

type sendWhatsAppTextMessageRequest struct {
	To        string `json:"to,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Message   string `json:"message,omitempty"`
	Content   string `json:"content,omitempty"`
}

type sendWhatsAppMediaMessageRequest struct {
	To           string `json:"to,omitempty"`
	Recipient    string `json:"recipient,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	MediaTypeAlt string `json:"mediaType,omitempty"`
	MediaID      string `json:"media_id,omitempty"`
	MediaIDAlt   string `json:"mediaId,omitempty"`
	Link         string `json:"link,omitempty"`
	MediaLink    string `json:"media_link,omitempty"`
	MediaLinkAlt string `json:"mediaLink,omitempty"`
	Caption      string `json:"caption,omitempty"`
	Filename     string `json:"filename,omitempty"`
}

type whatsAppConversationResponse struct {
	ID                     string     `json:"id"`
	TenantID               string     `json:"tenant_id"`
	DriverPhone            string     `json:"driver_phone"`
	DriverName             string     `json:"driver_name,omitempty"`
	LastInboundMessageAt   *time.Time `json:"last_inbound_message_at,omitempty"`
	LastOutboundMessageAt  *time.Time `json:"last_outbound_message_at,omitempty"`
	LastTemplateSentAt     *time.Time `json:"last_template_sent_at,omitempty"`
	WindowExpiresAt        *time.Time `json:"window_expires_at,omitempty"`
	WindowRemainingSeconds int64      `json:"window_remaining_seconds"`
	WindowRemainingHours   float64    `json:"window_remaining_hours"`
	IsLocked               bool       `json:"is_locked"`
	LastMessageAt          *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview     string     `json:"last_message_preview,omitempty"`
	LastMessageDirection   string     `json:"last_message_direction,omitempty"`
	LastMessageType        string     `json:"last_message_type,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type whatsAppConversationMessagesPayload struct {
	Conversation whatsAppConversationResponse         `json:"conversation"`
	Messages     []domain.WhatsAppConversationMessage `json:"messages"`
}

type whatsAppConversationSendPayload struct {
	OutboxMessage domain.OutboxMessage               `json:"outbox_message"`
	Conversation  whatsAppConversationResponse       `json:"conversation"`
	Message       domain.WhatsAppConversationMessage `json:"message"`
}

type Handler struct {
	sendMessageUseCase                      *application.SendMessageUseCase
	retryMessageUseCase                     *application.RetryMessageUseCase
	createWhatsAppAccountUseCase            *application.ConnectWhatsAppAccountUseCase
	getWhatsAppAccountUseCase               *application.GetWhatsAppAccountUseCase
	disconnectWhatsAppAccountUseCase        *application.DisconnectWhatsAppAccountUseCase
	createWhatsAppTemplateUseCase           *application.CreateWhatsAppTemplateUseCase
	listWhatsAppTemplatesUseCase            *application.ListWhatsAppTemplatesUseCase
	sendOtherTemplateUseCase                *application.SendOtherTemplateUseCase
	sendJobInstructionUseCase               *application.SendJobInstructionUseCase
	sendWhatsAppTextMessageUseCase          *application.SendWhatsAppTextMessageUseCase
	sendWhatsAppMediaMessageUseCase         *application.SendWhatsAppMediaMessageUseCase
	listWhatsAppConversationsUseCase        *application.ListWhatsAppConversationsUseCase
	getWhatsAppConversationUseCase          *application.GetWhatsAppConversationUseCase
	listWhatsAppConversationMessagesUseCase *application.ListWhatsAppConversationMessagesUseCase
	handleWhatsAppWebhookUseCase            *application.HandleWhatsAppWebhookUseCase
	listDeliveryLogUseCase                  *application.ListDeliveryLogUseCase
	activityPublisher                       activityPublisher
	clock                                   application.Clock
	whatsAppWebhookVerifyToken              string
	whatsAppAppSecret                       string
	handleSMSReceiptUseCase                 *application.HandleSMSDeliveryReceiptUseCase
	smsWebhookToken                         string
	campaignRepository                      application.CampaignRepository
	createCampaignUseCase                   *application.CreateCampaignUseCase
	sendCampaignUseCase                     *application.SendCampaignUseCase
	listCampaignsUseCase                    *application.ListCampaignsUseCase
	suppressRecipientUseCase                *application.SuppressRecipientUseCase
	platformInbox                           *application.PlatformInboxQueries
	whatsAppConnector                       *application.WhatsAppConnector
	platformOutreach                        *application.PlatformTemplateSendUseCase
	deleteWhatsAppTemplateUseCase           *application.DeleteWhatsAppTemplateUseCase
	inboxTools                              *PlatformInboxTools
	preferences                             *application.NotificationPreferencesUseCase
	deadLetters                             *application.DeadLetters
}

// WithCampaigns attaches the campaign use cases.
//
// A builder rather than more constructor arguments: the campaign routes report
// that they are unconfigured when it is never called, so a deployment without
// campaigns still starts.
func (h *Handler) WithCampaigns(
	repository application.CampaignRepository,
	create *application.CreateCampaignUseCase,
	send *application.SendCampaignUseCase,
	list *application.ListCampaignsUseCase,
	suppress *application.SuppressRecipientUseCase,
) *Handler {
	h.campaignRepository = repository
	h.createCampaignUseCase = create
	h.sendCampaignUseCase = send
	h.listCampaignsUseCase = list
	h.suppressRecipientUseCase = suppress
	return h
}

func NewHandler(
	sendMessageUseCase *application.SendMessageUseCase,
	retryMessageUseCase *application.RetryMessageUseCase,
	connectWhatsAppAccountUseCase *application.ConnectWhatsAppAccountUseCase,
	getWhatsAppAccountUseCase *application.GetWhatsAppAccountUseCase,
	disconnectWhatsAppAccountUseCase *application.DisconnectWhatsAppAccountUseCase,
	createWhatsAppTemplateUseCase *application.CreateWhatsAppTemplateUseCase,
	listWhatsAppTemplatesUseCase *application.ListWhatsAppTemplatesUseCase,
	sendOtherTemplateUseCase *application.SendOtherTemplateUseCase,
	sendJobInstructionUseCase *application.SendJobInstructionUseCase,
	sendWhatsAppTextMessageUseCase *application.SendWhatsAppTextMessageUseCase,
	sendWhatsAppMediaMessageUseCase *application.SendWhatsAppMediaMessageUseCase,
	listWhatsAppConversationsUseCase *application.ListWhatsAppConversationsUseCase,
	getWhatsAppConversationUseCase *application.GetWhatsAppConversationUseCase,
	listWhatsAppConversationMessagesUseCase *application.ListWhatsAppConversationMessagesUseCase,
	handleWhatsAppWebhookUseCase *application.HandleWhatsAppWebhookUseCase,
	listDeliveryLogUseCase *application.ListDeliveryLogUseCase,
	activityPublisher activityPublisher,
	clock application.Clock,
	whatsAppWebhookVerifyToken string,
	whatsAppAppSecret string,
) *Handler {
	return &Handler{
		sendMessageUseCase:                      sendMessageUseCase,
		retryMessageUseCase:                     retryMessageUseCase,
		createWhatsAppAccountUseCase:            connectWhatsAppAccountUseCase,
		getWhatsAppAccountUseCase:               getWhatsAppAccountUseCase,
		disconnectWhatsAppAccountUseCase:        disconnectWhatsAppAccountUseCase,
		createWhatsAppTemplateUseCase:           createWhatsAppTemplateUseCase,
		listWhatsAppTemplatesUseCase:            listWhatsAppTemplatesUseCase,
		sendOtherTemplateUseCase:                sendOtherTemplateUseCase,
		sendJobInstructionUseCase:               sendJobInstructionUseCase,
		sendWhatsAppTextMessageUseCase:          sendWhatsAppTextMessageUseCase,
		sendWhatsAppMediaMessageUseCase:         sendWhatsAppMediaMessageUseCase,
		listWhatsAppConversationsUseCase:        listWhatsAppConversationsUseCase,
		getWhatsAppConversationUseCase:          getWhatsAppConversationUseCase,
		listWhatsAppConversationMessagesUseCase: listWhatsAppConversationMessagesUseCase,
		handleWhatsAppWebhookUseCase:            handleWhatsAppWebhookUseCase,
		listDeliveryLogUseCase:                  listDeliveryLogUseCase,
		activityPublisher:                       activityPublisher,
		clock:                                   clock,
		whatsAppWebhookVerifyToken:              strings.TrimSpace(whatsAppWebhookVerifyToken),
		whatsAppAppSecret:                       strings.TrimSpace(whatsAppAppSecret),
	}
}

func (h *Handler) RegisterRoutes(routes *httpx.Routes) {
	routes.HandleFunc("/notify/health", []httpx.Audience{httpx.Public}, httpx.None(), h.health)
	routes.HandleFunc("/notify/docs", []httpx.Audience{httpx.Public}, httpx.None(), h.docsUI)
	routes.HandleFunc("/notify/docs/openapi.json", []httpx.Audience{httpx.Public}, httpx.None(), h.docsOpenAPI)
	routes.HandleFunc("/docs/notification", []httpx.Audience{httpx.Public}, httpx.None(), h.docsUI)
	routes.HandleFunc("/docs/notification/openapi.json", []httpx.Audience{httpx.Public}, httpx.None(), h.docsOpenAPI)
	routes.HandleFunc("/notify/messages/send", []httpx.Audience{httpx.Tenant, httpx.Service}, httpx.On("tenant.messaging"), h.sendMessage)
	routes.HandleFunc("/notify/messages/retry/", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.retryMessage)
	routes.HandleFunc("/notify/system/whatsapp/settings", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppSettings)
	routes.HandleFunc("/notify/system/whatsapp/settings/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppSettingsByPath)
	routes.HandleFunc("/notify/system/whatsapp/conversations", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppConversations)
	routes.HandleFunc("/notify/system/whatsapp/conversations/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppConversationByPath)
	routes.HandleFunc("/notify/system/whatsapp/templates", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppTemplates)
	routes.HandleFunc("/notify/system/whatsapp/templates/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppTemplateByPath)
	routes.HandleFunc("/notify/system/whatsapp/outreach", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppOutreach)
	routes.HandleFunc("/notify/system/voice/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemVoice)
	routes.HandleFunc("/notify/system/sms-templates", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemSMSTemplates)
	routes.HandleFunc("/notify/system/sms-templates/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemSMSTemplateByPath)
	routes.HandleFunc("/notify/system/campaigns", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemCampaigns)
	routes.HandleFunc("/notify/system/campaigns/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemCampaignByPath)
	routes.HandleFunc("/notify/system/whatsapp/media/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemWhatsAppMedia)
	routes.HandleFunc("/notify/system/whatsapp/canned-replies", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemCannedReplies)
	routes.HandleFunc("/notify/system/whatsapp/canned-replies/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemCannedReplyByPath)
	routes.HandleFunc("/notify/whatsapp/settings", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.whatsAppSettings)
	routes.HandleFunc("/notify/whatsapp/settings/", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.whatsAppSettingsByPath)
	routes.HandleFunc("/notify/whatsapp/create-template", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.createWhatsAppTemplate)
	routes.HandleFunc("/notify/whatsapp/get-all-templates", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.getAllWhatsAppTemplates)
	routes.HandleFunc("/notify/whatsapp/send-other-template", []httpx.Audience{httpx.Tenant, httpx.Service}, httpx.On("tenant.messaging"), h.sendOtherTemplate)
	routes.HandleFunc("/notify/whatsapp/send-job-instruction", []httpx.Audience{httpx.Tenant, httpx.Service}, httpx.On("tenant.messaging"), h.sendJobInstruction)
	routes.HandleFunc("/notify/whatsapp/send-message", []httpx.Audience{httpx.Tenant, httpx.Service}, httpx.On("tenant.messaging"), h.sendWhatsAppTextMessage)
	routes.HandleFunc("/notify/whatsapp/send-message-with-media", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.sendWhatsAppMediaMessage)
	routes.HandleFunc("/notify/whatsapp/conversations", []httpx.Audience{httpx.Tenant, httpx.Service}, httpx.On("tenant.messaging"), h.listWhatsAppConversations)
	routes.HandleFunc("/notify/whatsapp/conversations/", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.whatsAppConversationByPath)
	routes.HandleFunc("/notify/preferences", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.notificationPreferences)
	routes.HandleFunc("/notify/system/dead-letters", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemDeadLetters)
	routes.HandleFunc("/notify/system/dead-letters/", []httpx.Audience{httpx.Admin}, httpx.On("admin.messaging"), h.systemDeadLetterByPath)
	routes.HandleFunc("/notify/delivery-log", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.listDeliveryLog)
	routes.HandleFunc("/notify/campaigns", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.campaigns)
	routes.HandleFunc("/notify/campaigns/", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.campaignByPath)
	routes.HandleFunc("/notify/campaigns/suppressions", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.messaging"), h.campaignSuppressions)
	routes.HandleFunc("/notify/whatsapp/webhook", []httpx.Audience{httpx.Public}, httpx.None(), h.whatsAppWebhook)
	routes.HandleFunc("/notify/sms/webhook", []httpx.Audience{httpx.Public}, httpx.None(), h.smsWebhook)
	routes.HandleFunc("/health", []httpx.Audience{httpx.Public}, httpx.None(), h.health)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteSuccess(w, http.StatusOK, "ok", map[string]any{"service": "notification"})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := authenticatedTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	message, err := h.sendMessageUseCase.Execute(r.Context(), application.SendMessageCommand{
		TenantID:  tenantID,
		Channel:   req.Channel,
		Recipient: req.Recipient,
		Content:   req.Content,
	})

	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.WriteOK(w, http.StatusAccepted, message)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "message.sent",
		EntityType: "message",
		EntityID:   message.ID,
		Summary:    "Notification queued for delivery",
		Metadata: map[string]any{
			"channel":   req.Channel,
			"recipient": req.Recipient,
			"status":    message.Status,
		},
	})
}

func (h *Handler) retryMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := authenticatedTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/notify/messages/retry/")
	if id == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing message id")
		return
	}

	message, err := h.retryMessageUseCase.Execute(r.Context(), tenantID, id)
	if err == application.ErrMessageNotFound {
		httpx.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, application.ErrMessageNotRetryable) {
		httpx.WriteError(w, http.StatusConflict, err.Error())
		return
	}

	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.WriteOK(w, http.StatusOK, message)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   message.TenantID,
		Service:    "notification",
		Action:     "message.retried",
		EntityType: "message",
		EntityID:   message.ID,
		Summary:    "Notification retry requested",
		Metadata: map[string]any{
			"channel":   message.Channel,
			"recipient": message.Recipient,
			"status":    message.Status,
		},
	})
}

func (h *Handler) createWhatsAppTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := authenticatedTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	h.createWhatsAppTemplateFor(w, r, tenantID)
}

func (h *Handler) createWhatsAppTemplateFor(w http.ResponseWriter, r *http.Request, tenantID string) {
	var req createWhatsAppTemplateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	template, err := h.createWhatsAppTemplateUseCase.Execute(r.Context(), application.CreateWhatsAppTemplateCommand{
		TenantID:            tenantID,
		TemplateType:        firstNonEmpty(req.TemplateType, req.TemplateTypeAlt),
		Name:                req.Name,
		Header:              req.Header,
		Body:                req.Body,
		Language:            req.Language,
		Category:            req.Category,
		AllowCategoryChange: firstNonNilBool(req.AllowCategoryChange, req.AllowCategoryChangeAlt),
		ParameterFormat:     firstNonEmpty(req.ParameterFormat, req.ParameterFormatAlt),
		Components:          req.Components,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", template)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.template.created",
		EntityType: "whatsapp_template",
		EntityID:   firstNonEmpty(template.ID, req.Name),
		Summary:    "WhatsApp template submitted to Meta",
		Metadata: map[string]any{
			"name":          req.Name,
			"template_type": firstNonEmpty(req.TemplateType, req.TemplateTypeAlt),
			"status":        template.Status,
			"category":      template.Category,
		},
	})
}

func (h *Handler) getAllWhatsAppTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := authenticatedTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	h.listWhatsAppTemplatesFor(w, r, tenantID)
}

func (h *Handler) listWhatsAppTemplatesFor(w http.ResponseWriter, r *http.Request, tenantID string) {
	limit, after := parseTemplateListQuery(r)
	templates, err := h.listWhatsAppTemplatesUseCase.Execute(r.Context(), application.ListWhatsAppTemplatesQuery{TenantID: tenantID, Limit: limit, After: after})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", templates)
}

func (h *Handler) sendOtherTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	var req sendOtherTemplateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	message, err := h.sendOtherTemplateUseCase.Execute(r.Context(), application.SendOtherTemplateCommand{
		TenantID:         tenantID,
		Recipient:        firstNonEmpty(req.To, req.Recipient),
		TemplateName:     firstNonEmpty(req.TemplateName, req.TemplateNameAlt),
		Language:         req.Language,
		Variables:        normalizeOtherTemplateVariables(req.Variables, req.TemplateVariables, req.TemplateVariablesAlt),
		HeaderMedia:      normalizeJobInstructionHeaderMedia(firstNonNilHeaderMedia(req.HeaderMedia, req.HeaderMediaAlt)),
		ButtonParameters: normalizeJobInstructionButtonParameters(req.ButtonParameters, req.ButtonParametersAlt),
	})
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, application.ErrOtherTemplateTemplateNotFound):
			status = http.StatusNotFound
		case errors.Is(err, application.ErrOtherTemplateTemplateNotApproved),
			errors.Is(err, application.ErrOtherTemplateJobAssignmentNotAllowed),
			errors.Is(err, application.ErrOtherTemplateRecipientRequired),
			errors.Is(err, application.ErrOtherTemplateTemplateNameRequired),
			errors.Is(err, application.ErrOtherTemplateVariableRequired),
			errors.Is(err, application.ErrJobInstructionHeaderMediaRequired),
			errors.Is(err, application.ErrJobInstructionHeaderMediaTypeMismatch),
			errors.Is(err, application.ErrJobInstructionHeaderMediaValueRequired),
			errors.Is(err, application.ErrJobInstructionButtonParameterRequired),
			errors.Is(err, application.ErrJobInstructionButtonParameterValueNeeded):
			status = http.StatusBadRequest
		default:
			status = http.StatusBadGateway
		}
		httpx.WriteError(w, status, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", message)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.template.sent",
		EntityType: "message",
		EntityID:   message.ID,
		Summary:    "WhatsApp template sent to driver",
		Metadata: map[string]any{
			"template_name": firstNonEmpty(req.TemplateName, req.TemplateNameAlt),
			"recipient":     firstNonEmpty(req.To, req.Recipient),
			"status":        message.Status,
		},
	})
}

func (h *Handler) sendJobInstruction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	var req sendJobInstructionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	message, err := h.sendJobInstructionUseCase.Execute(r.Context(), application.SendJobInstructionCommand{
		TenantID:         tenantID,
		JobID:            firstNonEmpty(req.JobID, req.JobIDAlt),
		DriverID:         firstNonEmpty(req.DriverID, req.DriverIDAlt),
		Recipient:        firstNonEmpty(req.To, req.Recipient),
		TemplateName:     firstNonEmpty(req.TemplateName, req.TemplateNameAlt),
		Language:         req.Language,
		DriverName:       firstNonEmpty(req.DriverName, req.DriverNameAlt),
		JobReference:     firstNonEmpty(req.JobReference, req.JobReferenceAlt),
		PickupLocation:   firstNonEmpty(req.PickupLocation, req.PickupLocationAlt),
		DeliveryLocation: firstNonEmpty(req.DeliveryLocation, req.DeliveryLocationAlt),
		CargoDescription: firstNonEmpty(req.CargoDescription, req.CargoDescriptionAlt),
		Priority:         req.Priority,
		HeaderMedia:      normalizeJobInstructionHeaderMedia(firstNonNilHeaderMedia(req.HeaderMedia, req.HeaderMediaAlt)),
		ButtonParameters: normalizeJobInstructionButtonParameters(req.ButtonParameters, req.ButtonParametersAlt),
	})
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, application.ErrJobInstructionTemplateNotFound):
			status = http.StatusNotFound
		case errors.Is(err, application.ErrJobInstructionTemplateNotApproved),
			errors.Is(err, application.ErrJobInstructionTemplateNotCompatible),
			errors.Is(err, application.ErrJobInstructionRecipientRequired),
			errors.Is(err, application.ErrJobInstructionTemplateNameRequired),
			errors.Is(err, application.ErrJobInstructionDriverNameRequired),
			errors.Is(err, application.ErrJobInstructionReferenceRequired),
			errors.Is(err, application.ErrJobInstructionPickupLocationRequired),
			errors.Is(err, application.ErrJobInstructionDeliveryLocationRequired),
			errors.Is(err, application.ErrJobInstructionCargoDescriptionRequired),
			errors.Is(err, application.ErrJobInstructionPriorityRequired),
			errors.Is(err, application.ErrJobInstructionHeaderMediaRequired),
			errors.Is(err, application.ErrJobInstructionHeaderMediaTypeMismatch),
			errors.Is(err, application.ErrJobInstructionHeaderMediaValueRequired),
			errors.Is(err, application.ErrJobInstructionButtonParameterRequired),
			errors.Is(err, application.ErrJobInstructionButtonParameterValueNeeded):
			status = http.StatusBadRequest
		default:
			status = http.StatusBadGateway
		}
		httpx.WriteError(w, status, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", message)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.job_instruction.sent",
		EntityType: "message",
		EntityID:   message.ID,
		Summary:    "WhatsApp job instruction sent to driver",
		Metadata: map[string]any{
			"job_id":        firstNonEmpty(req.JobID, req.JobIDAlt),
			"driver_id":     firstNonEmpty(req.DriverID, req.DriverIDAlt),
			"template_name": firstNonEmpty(req.TemplateName, req.TemplateNameAlt),
			"recipient":     firstNonEmpty(req.To, req.Recipient),
			"status":        message.Status,
		},
	})
}

func (h *Handler) listWhatsAppConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	conversations, err := h.listWhatsAppConversationsUseCase.Execute(r.Context(), application.ListWhatsAppConversationsQuery{
		TenantID: tenantID,
		Query:    strings.TrimSpace(r.URL.Query().Get("query")),
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}

	now := h.clock.Now()
	response := make([]whatsAppConversationResponse, 0, len(conversations))
	for _, conversation := range conversations {
		response = append(response, buildWhatsAppConversationResponse(conversation, now))
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", response)
}

func (h *Handler) whatsAppConversationByPath(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/notify/whatsapp/conversations/"), "/")
	if trimmed == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 {
		h.getWhatsAppConversation(w, r, parts[0])
		return
	}
	if len(parts) == 2 && strings.EqualFold(parts[1], "messages") {
		h.listWhatsAppConversationMessages(w, r, parts[0])
		return
	}

	httpx.WriteError(w, http.StatusNotFound, "not found")
}

func (h *Handler) getWhatsAppConversation(w http.ResponseWriter, r *http.Request, conversationID string) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	conversation, err := h.getWhatsAppConversationUseCase.Execute(r.Context(), application.GetWhatsAppConversationQuery{
		TenantID:       tenantID,
		ConversationID: strings.TrimSpace(conversationID),
	})
	if err != nil {
		httpx.WriteError(w, whatsAppConversationErrorStatus(err), err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", buildWhatsAppConversationResponse(conversation, h.clock.Now()))
}

func (h *Handler) listWhatsAppConversationMessages(w http.ResponseWriter, r *http.Request, conversationID string) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	conversation, messages, err := h.listWhatsAppConversationMessagesUseCase.Execute(r.Context(), application.ListWhatsAppConversationMessagesQuery{
		TenantID:       tenantID,
		ConversationID: strings.TrimSpace(conversationID),
	})
	if err != nil {
		httpx.WriteError(w, whatsAppConversationErrorStatus(err), err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", whatsAppConversationMessagesPayload{
		Conversation: buildWhatsAppConversationResponse(conversation, h.clock.Now()),
		Messages:     messages,
	})
}

func (h *Handler) sendWhatsAppTextMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	var req sendWhatsAppTextMessageRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	outboxMessage, conversation, message, err := h.sendWhatsAppTextMessageUseCase.Execute(r.Context(), application.SendWhatsAppTextMessageCommand{
		TenantID:  tenantID,
		Recipient: firstNonEmpty(req.To, req.Recipient),
		Message:   firstNonEmpty(req.Message, req.Content),
	})
	if err != nil {
		httpx.WriteError(w, whatsAppConversationErrorStatus(err), err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", whatsAppConversationSendPayload{
		OutboxMessage: outboxMessage,
		Conversation:  buildWhatsAppConversationResponse(conversation, h.clock.Now()),
		Message:       message,
	})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.message.sent",
		EntityType: "message",
		EntityID:   outboxMessage.ID,
		Summary:    "WhatsApp text message sent to driver",
		Metadata: map[string]any{
			"conversation_id": conversation.ID,
			"recipient":       firstNonEmpty(req.To, req.Recipient),
			"status":          outboxMessage.Status,
		},
	})
}

func (h *Handler) sendWhatsAppMediaMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	var req sendWhatsAppMediaMessageRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	outboxMessage, conversation, message, err := h.sendWhatsAppMediaMessageUseCase.Execute(r.Context(), application.SendWhatsAppMediaMessageCommand{
		TenantID:  tenantID,
		Recipient: firstNonEmpty(req.To, req.Recipient),
		MediaType: firstNonEmpty(req.MediaType, req.MediaTypeAlt),
		MediaID:   firstNonEmpty(req.MediaID, req.MediaIDAlt),
		MediaLink: firstNonEmpty(req.Link, req.MediaLink, req.MediaLinkAlt),
		Caption:   req.Caption,
		Filename:  req.Filename,
	})
	if err != nil {
		httpx.WriteError(w, whatsAppConversationErrorStatus(err), err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", whatsAppConversationSendPayload{
		OutboxMessage: outboxMessage,
		Conversation:  buildWhatsAppConversationResponse(conversation, h.clock.Now()),
		Message:       message,
	})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.media_message.sent",
		EntityType: "message",
		EntityID:   outboxMessage.ID,
		Summary:    "WhatsApp media message sent to driver",
		Metadata: map[string]any{
			"conversation_id": conversation.ID,
			"recipient":       firstNonEmpty(req.To, req.Recipient),
			"media_type":      firstNonEmpty(req.MediaType, req.MediaTypeAlt),
			"status":          outboxMessage.Status,
		},
	})
}

func (h *Handler) whatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.verifyWhatsAppWebhook(w, r)
	case http.MethodPost:
		h.handleWhatsAppWebhook(w, r)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) verifyWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	if h.whatsAppWebhookVerifyToken == "" {
		httpx.WriteError(w, http.StatusInternalServerError, "whatsapp webhook verify token is not configured")
		return
	}

	mode := strings.TrimSpace(r.URL.Query().Get("hub.mode"))
	verifyToken := strings.TrimSpace(r.URL.Query().Get("hub.verify_token"))
	challenge := strings.TrimSpace(r.URL.Query().Get("hub.challenge"))
	if mode != "subscribe" || challenge == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid webhook verification request")
		return
	}
	if subtle.ConstantTimeCompare([]byte(verifyToken), []byte(h.whatsAppWebhookVerifyToken)) != 1 {
		httpx.WriteError(w, http.StatusForbidden, "invalid webhook verify token")
		return
	}

	// Meta's verification handshake expects the challenge echoed as plain text.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)      // contract:raw webhook challenge
	_, _ = w.Write([]byte(challenge)) // contract:raw webhook challenge
}

const whatsAppSignatureHeader = "X-Hub-Signature-256"

// whatsAppSignatureValid reports whether Meta signed this request body.
//
// The verify token on the GET handshake only proves we own the callback URL at
// subscription time. It says nothing about the POSTs that follow, and this
// endpoint has to stay publicly reachable for Meta to call it. Without this
// check anyone who learns the URL can inject inbound messages into a tenant's
// conversation and forge delivery receipts on real ones.
func (h *Handler) whatsAppSignatureValid(header string, body []byte) bool {
	// An unset secret refuses every delivery rather than accepting every one.
	// A misconfigured deployment must not read as an open door.
	if h.whatsAppAppSecret == "" {
		return false
	}

	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.whatsAppAppSecret))
	mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func (h *Handler) handleWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Before the body is parsed, not after. The signature covers these exact
	// bytes, so any decode and re-encode would change what is being checked.
	if !h.whatsAppSignatureValid(r.Header.Get(whatsAppSignatureHeader), payload) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	if !json.Valid(payload) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	processed, err := h.handleWhatsAppWebhookUseCase.Execute(r.Context(), application.HandleWhatsAppWebhookCommand{Payload: payload})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "received", map[string]any{"processed": processed})
}

// WithSMSReceipts turns on the Dotgo delivery receipt route. Until it is called
// the route refuses every request, so a deployment with no token configured is
// closed rather than open.
func (h *Handler) WithSMSReceipts(useCase *application.HandleSMSDeliveryReceiptUseCase, token string) *Handler {
	h.handleSMSReceiptUseCase = useCase
	h.smsWebhookToken = strings.TrimSpace(token)
	return h
}

// maxSMSWebhookBytes bounds a receipt body. A receipt is a few fields, so this
// is generous, and the route is public.
const maxSMSWebhookBytes = 1 << 20

// smsWebhook receives Dotgo's delivery receipts for messages this service sent.
//
// Dotgo does not sign its callbacks, so the URL it was given carries a secret
// token and this route refuses a request without it. The route has to stay
// publicly reachable for Dotgo to call it; without the token anyone who learns
// the address could mark real messages failed.
func (h *Handler) smsWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.smsWebhookTokenValid(r.URL.Query().Get("token")) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid webhook token")
		return
	}
	if h.handleSMSReceiptUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "sms delivery receipts are not configured")
		return
	}

	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSMSWebhookBytes))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	receipts, ok := parseSMSReceipts(payload)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	processed := 0
	for _, receipt := range receipts {
		matched, err := h.handleSMSReceiptUseCase.Execute(r.Context(), receipt)
		if err != nil {
			// A failure here is ours, so Dotgo is told to send it again.
			httpx.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		if matched {
			processed++
		}
	}

	// A receipt for a message this service did not send is still a 200: Dotgo
	// cannot do anything about it and would only retry.
	httpx.WriteSuccess(w, http.StatusOK, "received", map[string]any{"processed": processed})
}

// smsWebhookTokenValid compares in constant time. An unset token refuses every
// request rather than accepting every one.
func (h *Handler) smsWebhookTokenValid(provided string) bool {
	if h.smsWebhookToken == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.smsWebhookToken)) == 1
}

// parseSMSReceipts reads Dotgo's callback body, which is one receipt, a list of
// them, or either wrapped in a JSON string. Numbers are kept as written so a
// numeric request id is not turned into a float.
func parseSMSReceipts(payload []byte) ([]application.SMSDeliveryReceipt, bool) {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	if text, isText := decoded.(string); isText {
		return parseSMSReceipts([]byte(text))
	}

	var items []any
	switch value := decoded.(type) {
	case map[string]any:
		items = []any{value}
	case []any:
		items = value
	default:
		return nil, false
	}

	receipts := make([]application.SMSDeliveryReceipt, 0, len(items))
	for _, item := range items {
		fields, isObject := item.(map[string]any)
		if !isObject {
			continue
		}
		receipts = append(receipts, application.SMSDeliveryReceipt{
			ID:          receiptText(fields["id"]),
			Status:      receiptText(fields["status"]),
			ErrorReason: receiptText(fields["error_reason"]),
		})
	}
	return receipts, true
}

func receiptText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func (h *Handler) publishActivity(r *http.Request, command activitylog.PublishCommand) {
	if h.activityPublisher == nil {
		return
	}

	if claims, ok := httpx.ClaimsFromRequest(r); ok && claims != nil {
		if command.ActorID == "" {
			command.ActorID = firstNonEmpty(strings.TrimSpace(claims.Subject), strings.TrimSpace(claims.Email))
		}
		if command.ActorRole == "" {
			command.ActorRole = strings.TrimSpace(claims.Role)
		}
	}

	if command.OccurredAt.IsZero() {
		command.OccurredAt = h.clock.Now()
	}

	if err := h.activityPublisher.Publish(r.Context(), command); err != nil {
		// Activity logging is best-effort and should not fail the main request path.
	}
}

func buildWhatsAppConversationResponse(conversation domain.WhatsAppConversation, now time.Time) whatsAppConversationResponse {
	return whatsAppConversationResponse{
		ID:                     conversation.ID,
		TenantID:               conversation.TenantID,
		DriverPhone:            conversation.DriverPhone,
		DriverName:             conversation.DriverName,
		LastInboundMessageAt:   conversation.LastInboundMessageAt,
		LastOutboundMessageAt:  conversation.LastOutboundMessageAt,
		LastTemplateSentAt:     conversation.LastTemplateSentAt,
		WindowExpiresAt:        conversation.WindowExpiresAt,
		WindowRemainingSeconds: conversation.WindowRemainingSeconds(now),
		WindowRemainingHours:   conversation.WindowRemainingHours(now),
		IsLocked:               conversation.IsLocked(now),
		LastMessageAt:          conversation.LastMessageAt,
		LastMessagePreview:     conversation.LastMessagePreview,
		LastMessageDirection:   conversation.LastMessageDirection,
		LastMessageType:        conversation.LastMessageType,
		CreatedAt:              conversation.CreatedAt,
		UpdatedAt:              conversation.UpdatedAt,
	}
}

func whatsAppConversationErrorStatus(err error) int {
	switch {
	case errors.Is(err, application.ErrWhatsAppConversationNotFound):
		return http.StatusNotFound
	case errors.Is(err, application.ErrWhatsAppConversationLocked):
		return http.StatusConflict
	case errors.Is(err, application.ErrWhatsAppConversationIDRequired),
		errors.Is(err, application.ErrWhatsAppMessageRequired),
		errors.Is(err, application.ErrWhatsAppMediaTypeRequired),
		errors.Is(err, application.ErrWhatsAppMediaTypeUnsupported),
		errors.Is(err, application.ErrWhatsAppMediaValueRequired),
		errors.Is(err, application.ErrOtherTemplateRecipientRequired):
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

func normalizeOtherTemplateVariables(values ...map[string]string) map[string]string {
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		normalized := make(map[string]string, len(value))
		for key, item := range value {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			normalized[trimmedKey] = strings.TrimSpace(item)
		}
		if len(normalized) > 0 {
			return normalized
		}
	}
	return nil
}

func normalizeJobInstructionHeaderMedia(headerMedia *sendJobInstructionHeaderMediaRequest) *application.JobInstructionHeaderMedia {
	if headerMedia == nil {
		return nil
	}
	return &application.JobInstructionHeaderMedia{
		Type:     headerMedia.Type,
		Link:     headerMedia.Link,
		ID:       headerMedia.ID,
		Filename: headerMedia.Filename,
	}
}

func normalizeJobInstructionButtonParameters(primary, alternate []sendJobInstructionButtonParameterRequest) []application.JobInstructionButtonParameter {
	requests := primary
	if len(requests) == 0 {
		requests = alternate
	}
	if len(requests) == 0 {
		return nil
	}
	parameters := make([]application.JobInstructionButtonParameter, 0, len(requests))
	for _, request := range requests {
		parameters = append(parameters, application.JobInstructionButtonParameter{
			Index:   request.Index,
			Type:    request.Type,
			Text:    request.Text,
			Payload: request.Payload,
		})
	}
	return parameters
}

func authenticatedTenantID(r *http.Request) (string, bool) {
	tenantID := strings.TrimSpace(httpx.TenantIDFromRequest(r))
	if tenantID == "" {
		return "", false
	}
	return tenantID, true
}

func authenticatedDispatcherTenantID(r *http.Request) (string, int, bool) {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || strings.TrimSpace(claims.TenantID) == "" {
		return "", http.StatusUnauthorized, false
	}
	if !isDispatcherCapableRole(claims.Role) {
		return "", http.StatusForbidden, false
	}
	return strings.TrimSpace(claims.TenantID), http.StatusOK, true
}

func isDispatcherCapableRole(role string) bool {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "DISPATCHER", "OPERATIONS_MANAGER", "FLEET_ADMIN", "PLATFORM_ADMIN", "ENTERPRISE_ADMIN":
		return true
	default:
		return false
	}
}

func notificationAuthMessage(status int) string {
	if status == http.StatusForbidden {
		return "dispatcher role required"
	}
	return "authentication required"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNilBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilHeaderMedia(values ...*sendJobInstructionHeaderMediaRequest) *sendJobInstructionHeaderMediaRequest {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
