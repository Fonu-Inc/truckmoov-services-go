package notificationhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	"github.com/truckmoov/services-go/pkg/activitylog"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type fakeNotificationProvider struct {
	status               string
	err                  error
	counter              int
	genericSendCount     int
	lastGenericTenantID  string
	lastGenericChannel   string
	lastGenericRecipient string
	lastGenericContent   string
	lastTemplateTenantID string
	lastTemplateRequest  domain.WhatsAppTemplateMessageRequest
	lastTemplateResult   domain.WhatsAppSendResult
	lastTextTenantID     string
	lastTextRecipient    string
	lastTextContent      string
	lastTextResult       domain.WhatsAppSendResult
	lastMediaRequest     domain.WhatsAppMediaMessageRequest
	lastMediaResult      domain.WhatsAppSendResult
}

func (f *fakeNotificationProvider) Send(_ context.Context, tenantID, channel, recipient, content string) (string, error) {
	return f.send(tenantID, channel, recipient, content)
}

func (f *fakeNotificationProvider) send(tenantID, channel, recipient, content string) (string, error) {
	f.genericSendCount++
	f.lastGenericTenantID = tenantID
	f.lastGenericChannel = channel
	f.lastGenericRecipient = recipient
	f.lastGenericContent = content
	status := f.status
	if status == "" {
		status = domain.StatusSent
	}
	return status, f.err
}

func (f *fakeNotificationProvider) SendTemplate(_ context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error) {
	f.counter++
	f.lastTemplateTenantID = request.TenantID
	f.lastTemplateRequest = request
	result := domain.WhatsAppSendResult{Status: domain.StatusSent, MessageID: fmt.Sprintf("wamid.template.%d", f.counter)}
	if strings.TrimSpace(f.status) != "" {
		result.Status = f.status
	}
	f.lastTemplateResult = result
	if f.err != nil {
		result.Status = domain.StatusFailed
	}
	return result, f.err
}

func (f *fakeNotificationProvider) SendText(_ context.Context, tenantID, recipient, content string) (domain.WhatsAppSendResult, error) {
	f.counter++
	f.lastTextTenantID = tenantID
	f.lastTextRecipient = recipient
	f.lastTextContent = content
	result := domain.WhatsAppSendResult{Status: domain.StatusSent, MessageID: fmt.Sprintf("wamid.text.%d", f.counter)}
	if strings.TrimSpace(f.status) != "" {
		result.Status = f.status
	}
	f.lastTextResult = result
	if f.err != nil {
		result.Status = domain.StatusFailed
	}
	return result, f.err
}

func (f *fakeNotificationProvider) SendMedia(_ context.Context, request domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error) {
	f.counter++
	f.lastMediaRequest = request
	result := domain.WhatsAppSendResult{Status: domain.StatusSent, MessageID: fmt.Sprintf("wamid.media.%d", f.counter)}
	if strings.TrimSpace(f.status) != "" {
		result.Status = f.status
	}
	f.lastMediaResult = result
	if f.err != nil {
		result.Status = domain.StatusFailed
	}
	return result, f.err
}

type fakeTemplateProvider struct {
	createResponse domain.WhatsAppTemplateCreateResponse
	listResponse   domain.WhatsAppTemplateListResponse
	createErr      error
	listErr        error
	lastCreate     domain.WhatsAppTemplateCreateRequest
	lastList       domain.WhatsAppTemplateListRequest
}

func (f *fakeTemplateProvider) CreateTemplate(_ context.Context, request domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error) {
	f.lastCreate = request
	return f.createResponse, f.createErr
}

func (f *fakeTemplateProvider) ListTemplates(_ context.Context, request domain.WhatsAppTemplateListRequest) (domain.WhatsAppTemplateListResponse, error) {
	f.lastList = request
	return f.listResponse, f.listErr
}

type mutableNotificationClock struct{ now time.Time }

func (f *mutableNotificationClock) Now() time.Time { return f.now }

type sequentialNotificationID struct{ next int }

func (f *sequentialNotificationID) New(prefix string) string {
	f.next++
	return fmt.Sprintf("%s-%d", prefix, f.next)
}

type fakeNotificationPublisher struct {
	events []domain.MessageDeliveryUpdated
}

func (f *fakeNotificationPublisher) PublishDeliveryUpdated(_ context.Context, event domain.MessageDeliveryUpdated) error {
	f.events = append(f.events, event)
	return nil
}

type fakeDriverStatusRequestSender struct {
	commands []application.CreateDriverStatusRequestFromWhatsAppCommand
	result   application.DriverStatusRequestReceipt
	err      error
}

type fakeActivityPublisher struct {
	commands []activitylog.PublishCommand
}

func (f *fakeActivityPublisher) Publish(_ context.Context, command activitylog.PublishCommand) error {
	f.commands = append(f.commands, command)
	return nil
}

type fakeWhatsAppEmbeddedSignupProvider struct{}

func (f *fakeWhatsAppEmbeddedSignupProvider) ExchangeCodeForLongLivedToken(_ context.Context, code string) (string, error) {
	return "token-for-" + strings.TrimSpace(code), nil
}

func (f *fakeWhatsAppEmbeddedSignupProvider) GetPhoneNumberProfile(_ context.Context, _ string, phoneNumberID string) (domain.WhatsAppAccount, error) {
	return domain.WhatsAppAccount{
		PhoneNumberID:      strings.TrimSpace(phoneNumberID),
		DisplayPhoneNumber: "+2348012345678",
		VerifiedName:       "Truckmoov Support",
	}, nil
}

func (f *fakeWhatsAppEmbeddedSignupProvider) GetBusinessProfile(_ context.Context, _ string, businessAccountID string) (domain.WhatsAppAccount, error) {
	return domain.WhatsAppAccount{
		BusinessAccountID: strings.TrimSpace(businessAccountID),
		BusinessName:      "Truckmoov",
	}, nil
}

func (f *fakeWhatsAppEmbeddedSignupProvider) SubscribeBusinessApp(_ context.Context, _, _ string) error {
	return nil
}

type fakeWhatsAppTokenCipher struct{}

func (f *fakeWhatsAppTokenCipher) Encrypt(token string) (string, string, error) {
	return "enc:" + token, "iv", nil
}

func (f *fakeWhatsAppTokenCipher) Decrypt(ciphertext, _ string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

func (f *fakeDriverStatusRequestSender) CreateDriverStatusRequest(_ context.Context, command application.CreateDriverStatusRequestFromWhatsAppCommand) (application.DriverStatusRequestReceipt, error) {
	f.commands = append(f.commands, command)
	if f.err != nil {
		return application.DriverStatusRequestReceipt{}, f.err
	}
	if strings.TrimSpace(f.result.RequestID) == "" {
		f.result = application.DriverStatusRequestReceipt{
			RequestID:       "req-1",
			RequestedStatus: "en-route",
		}
	}
	return f.result, nil
}

type notificationEnvelope struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func notificationHandlerForTests(t *testing.T) (http.Handler, *fakeTemplateProvider, *fakeNotificationProvider, *mutableNotificationClock, *infrastructure.InMemoryOutboxRepository, *fakeDriverStatusRequestSender, *fakeActivityPublisher) {
	t.Helper()

	outboxRepository := infrastructure.NewInMemoryOutboxRepository()
	conversationRepository := infrastructure.NewInMemoryWhatsAppConversationRepository()
	whatsAppAccountRepository := infrastructure.NewInMemoryWhatsAppAccountRepository()
	provider := &fakeNotificationProvider{status: domain.StatusSent}
	signupProvider := &fakeWhatsAppEmbeddedSignupProvider{}
	tokenCipher := &fakeWhatsAppTokenCipher{}
	templateProvider := &fakeTemplateProvider{
		createResponse: domain.WhatsAppTemplateCreateResponse{ID: "tpl-1", Status: "PENDING", Category: "UTILITY"},
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{
			{
				ID:       "tpl-job",
				Name:     "job_assignment_v1",
				Language: "en_US",
				Status:   "APPROVED",
				Category: "UTILITY",
				Components: []map[string]any{
					{"type": "BODY", "text": "Driver {{1}}, job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}"},
				},
			},
			{
				ID:       "tpl-other",
				Name:     "driver_followup_v1",
				Language: "en_US",
				Status:   "APPROVED",
				Category: "UTILITY",
				Components: []map[string]any{
					{"type": "BODY", "text": "Hello {{1}}, report to {{2}} before {{3}}"},
				},
			},
		}},
	}
	publisher := &fakeNotificationPublisher{}
	driverStatusRequestSender := &fakeDriverStatusRequestSender{}
	activityPublisher := &fakeActivityPublisher{}
	clock := &mutableNotificationClock{now: time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)}
	ids := &sequentialNotificationID{}

	sendUseCase := application.NewSendMessageUseCase(outboxRepository, provider, publisher, clock, ids, "default")
	retryUseCase := application.NewRetryMessageUseCase(outboxRepository, provider, publisher, clock, ids)
	connectWhatsAppAccountUseCase := application.NewConnectWhatsAppAccountUseCase(whatsAppAccountRepository, signupProvider, tokenCipher, clock, ids, "default")
	getWhatsAppAccountUseCase := application.NewGetWhatsAppAccountUseCase(whatsAppAccountRepository, "default")
	disconnectWhatsAppAccountUseCase := application.NewDisconnectWhatsAppAccountUseCase(whatsAppAccountRepository, "default")
	createTemplateUseCase := application.NewCreateWhatsAppTemplateUseCase(templateProvider)
	listTemplatesUseCase := application.NewListWhatsAppTemplatesUseCase(templateProvider)
	sendOtherTemplateUseCase := application.NewSendOtherTemplateUseCase(outboxRepository, conversationRepository, templateProvider, provider, publisher, clock, ids, "default")
	sendJobInstructionUseCase := application.NewSendJobInstructionUseCase(outboxRepository, conversationRepository, templateProvider, provider, publisher, clock, ids, "default")
	sendWhatsAppTextMessageUseCase := application.NewSendWhatsAppTextMessageUseCase(outboxRepository, conversationRepository, provider, publisher, clock, ids, "default")
	sendWhatsAppMediaMessageUseCase := application.NewSendWhatsAppMediaMessageUseCase(outboxRepository, conversationRepository, provider, publisher, clock, ids, "default")
	listConversationsUseCase := application.NewListWhatsAppConversationsUseCase(conversationRepository, clock, "default")
	getConversationUseCase := application.NewGetWhatsAppConversationUseCase(conversationRepository, "default")
	listConversationMessagesUseCase := application.NewListWhatsAppConversationMessagesUseCase(conversationRepository, "default")
	handleWebhookUseCase := application.NewHandleWhatsAppWebhookUseCase(conversationRepository, outboxRepository, whatsAppAccountRepository, provider, driverStatusRequestSender, publisher, activityPublisher, clock, ids)

	handler := NewHandler(
		sendUseCase,
		retryUseCase,
		connectWhatsAppAccountUseCase,
		getWhatsAppAccountUseCase,
		disconnectWhatsAppAccountUseCase,
		createTemplateUseCase,
		listTemplatesUseCase,
		sendOtherTemplateUseCase,
		sendJobInstructionUseCase,
		sendWhatsAppTextMessageUseCase,
		sendWhatsAppMediaMessageUseCase,
		listConversationsUseCase,
		getConversationUseCase,
		listConversationMessagesUseCase,
		handleWebhookUseCase,
		activityPublisher,
		clock,
		"verify-token",
	)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return httpx.WithAuthContext(mux, "replace-me"), templateProvider, provider, clock, outboxRepository, driverStatusRequestSender, activityPublisher
}

func makeNotificationAuthRequest(method, path string, body []byte, tenantID string) *http.Request {
	return makeNotificationAuthRequestWithRole(method, path, body, tenantID, "DISPATCHER")
}

func makeNotificationAuthRequestWithRole(method, path string, body []byte, tenantID, role string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("Authorization", "Bearer "+makeNotificationToken("replace-me", tenantID, role))
	}
	return req
}

func makeNotificationToken(secret, tenantID, role string) string {
	payload := fmt.Sprintf(`{"sub":"user-1","email":"ops@example.com","role":"%s","tenantId":"%s","exp":%d}`,
		role,
		tenantID,
		time.Now().Add(time.Hour).Unix(),
	)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature
}

func decodeNotificationEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) notificationEnvelope {
	t.Helper()
	var envelope notificationEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

func listNotificationConversations(t *testing.T, handler http.Handler, tenantID string) []whatsAppConversationResponse {
	t.Helper()
	req := makeNotificationAuthRequest(http.MethodGet, "/notify/whatsapp/conversations", nil, tenantID)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, res.Code, res.Body.String())
	}
	envelope := decodeNotificationEnvelope(t, res)
	var conversations []whatsAppConversationResponse
	if err := json.Unmarshal(envelope.Data, &conversations); err != nil {
		t.Fatalf("decode conversation list: %v", err)
	}
	return conversations
}

func sendJobInstructionForTests(t *testing.T, handler http.Handler, tenantID string) {
	t.Helper()
	payload := []byte(`{
		"jobId":"job-1",
		"driverId":"driver-1",
		"to":"+2348012345678",
		"templateName":"job_assignment_v1",
		"driverName":"Chukwuma Okafor",
		"jobReference":"TM-2847",
		"pickupLocation":"Apapa Port, Lagos",
		"deliveryLocation":"Ikeja Industrial Estate",
		"cargoDescription":"Electronics, 50kg",
		"priority":"urgent"
	}`)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/whatsapp/send-job-instruction", payload, tenantID)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, res.Code, res.Body.String())
	}
}

func TestWhatsAppTemplateEndpointsRequireAuthentication(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/whatsapp/get-all-templates", nil, "")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, res.Code)
	}
	envelope := decodeNotificationEnvelope(t, res)
	if envelope.Status {
		t.Fatal("expected unsuccessful response")
	}
	if envelope.Message != "authentication required" {
		t.Fatalf("expected authentication required message, got %q", envelope.Message)
	}
}

func TestSendMessage_RequiresAuth(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/messages/send", []byte(`{
		"tenant_id":"tenant-a",
		"channel":"sms",
		"recipient":"+23400000000",
		"content":"hello"
	}`), "")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d: %s", http.StatusUnauthorized, res.Code, res.Body.String())
	}
}

func TestRetryMessage_RequiresAuth(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/messages/retry/msg-1", nil, "")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d: %s", http.StatusUnauthorized, res.Code, res.Body.String())
	}
}

func TestWhatsAppSettingsConnectGetAndDisconnect(t *testing.T) {
	handler, _, _, _, _, _, activityPublisher := notificationHandlerForTests(t)

	connectReq := makeNotificationAuthRequestWithRole(http.MethodPost, "/notify/whatsapp/settings", []byte(`{
		"phoneNumberId":"pnid-123",
		"businessAccountId":"waba-123",
		"code":"embedded-signup-code"
	}`), "tenant-a", "PLATFORM_ADMIN")
	connectRes := httptest.NewRecorder()
	handler.ServeHTTP(connectRes, connectReq)
	if connectRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, connectRes.Code, connectRes.Body.String())
	}
	connectEnvelope := decodeNotificationEnvelope(t, connectRes)
	var account domain.WhatsAppAccount
	if err := json.Unmarshal(connectEnvelope.Data, &account); err != nil {
		t.Fatalf("decode whatsapp account: %v", err)
	}
	if account.PhoneNumberID != "pnid-123" {
		t.Fatalf("expected phone number id pnid-123, got %q", account.PhoneNumberID)
	}
	if account.BusinessAccountID != "waba-123" {
		t.Fatalf("expected business account id waba-123, got %q", account.BusinessAccountID)
	}
	if account.EncryptedAccessToken != "" || account.AccessTokenIV != "" {
		t.Fatal("expected sensitive token fields to be omitted from response")
	}

	getReq := makeNotificationAuthRequestWithRole(http.MethodGet, "/notify/whatsapp/settings", nil, "tenant-a", "OPERATIONS_MANAGER")
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, getRes.Code, getRes.Body.String())
	}
	getEnvelope := decodeNotificationEnvelope(t, getRes)
	if err := json.Unmarshal(getEnvelope.Data, &account); err != nil {
		t.Fatalf("decode whatsapp account from get: %v", err)
	}
	if account.DisplayPhoneNumber != "+2348012345678" {
		t.Fatalf("expected populated display phone number, got %q", account.DisplayPhoneNumber)
	}

	deleteReq := makeNotificationAuthRequestWithRole(http.MethodDelete, "/notify/whatsapp/settings/pnid-123", nil, "tenant-a", "PLATFORM_ADMIN")
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, deleteRes.Code, deleteRes.Body.String())
	}

	getRes = httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusNotFound {
		t.Fatalf("expected status %d after disconnect, got %d: %s", http.StatusNotFound, getRes.Code, getRes.Body.String())
	}

	var connectedLogged bool
	var disconnectedLogged bool
	for _, command := range activityPublisher.commands {
		switch command.Action {
		case "whatsapp.account.connected":
			connectedLogged = true
		case "whatsapp.account.disconnected":
			disconnectedLogged = true
		}
	}
	if !connectedLogged || !disconnectedLogged {
		t.Fatalf("expected connect and disconnect activity logs, got %#v", activityPublisher.commands)
	}
}

func TestWhatsAppSettingsRequireAdministratorRole(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequestWithRole(http.MethodGet, "/notify/whatsapp/settings", nil, "tenant-a", "DISPATCHER")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, res.Code, res.Body.String())
	}
	envelope := decodeNotificationEnvelope(t, res)
	if envelope.Message != "administrator role required" {
		t.Fatalf("expected administrator role required message, got %q", envelope.Message)
	}
}

func TestSendJobInstructionCreatesLockedConversation(t *testing.T) {
	handler, _, provider, _, _, _, _ := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")

	conversations := listNotificationConversations(t, handler, "tenant-a")
	if len(conversations) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(conversations))
	}
	if !conversations[0].IsLocked {
		t.Fatal("expected conversation to be locked after template send")
	}
	if conversations[0].WindowRemainingSeconds != 0 {
		t.Fatalf("expected locked conversation to have no open window, got %d", conversations[0].WindowRemainingSeconds)
	}
	if provider.lastTemplateRequest.Name != "job_assignment_v1" {
		t.Fatalf("expected template job_assignment_v1, got %q", provider.lastTemplateRequest.Name)
	}
	if provider.lastTemplateResult.MessageID == "" {
		t.Fatal("expected outbound template message id to be captured")
	}
}

func TestSendWhatsAppTextMessageRequiresOpenConversation(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")

	payload := []byte(`{"to":"+2348012345678","message":"Please confirm receipt"}`)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/whatsapp/send-message", payload, "tenant-a")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, res.Code, res.Body.String())
	}
	envelope := decodeNotificationEnvelope(t, res)
	if !strings.Contains(envelope.Message, "locked") {
		t.Fatalf("expected locked conversation error, got %#v", envelope)
	}
}

func TestWhatsAppWebhookReplyOpensConversationAndAllowsTextAndMediaSend(t *testing.T) {
	handler, _, provider, clock, _, _, activityPublisher := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")
	conversations := listNotificationConversations(t, handler, "tenant-a")
	conversationID := conversations[0].ID
	clock.now = clock.now.Add(5 * time.Minute)
	webhookPayload := fmt.Sprintf(`{
		"entry":[{
			"changes":[{
				"value":{
					"contacts":[{"profile":{"name":"Chukwuma Okafor"},"wa_id":"2348012345678"}],
					"messages":[{
						"id":"wamid.inbound.1",
						"from":"2348012345678",
						"timestamp":"%d",
						"type":"text",
						"context":{"id":"%s"},
						"text":{"body":"Received, I am on it"}
					}]
				}
			}]
		}]
	}`,
		clock.now.Unix(),
		provider.lastTemplateResult.MessageID,
	)
	webhookReq := httptest.NewRequest(http.MethodPost, "/notify/whatsapp/webhook", strings.NewReader(webhookPayload))
	webhookReq.Header.Set("Content-Type", "application/json")
	webhookRes := httptest.NewRecorder()
	handler.ServeHTTP(webhookRes, webhookReq)
	if webhookRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, webhookRes.Code, webhookRes.Body.String())
	}

	conversationReq := makeNotificationAuthRequest(http.MethodGet, "/notify/whatsapp/conversations/"+conversationID, nil, "tenant-a")
	conversationRes := httptest.NewRecorder()
	handler.ServeHTTP(conversationRes, conversationReq)
	if conversationRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, conversationRes.Code, conversationRes.Body.String())
	}
	conversationEnvelope := decodeNotificationEnvelope(t, conversationRes)
	var conversation whatsAppConversationResponse
	if err := json.Unmarshal(conversationEnvelope.Data, &conversation); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if conversation.IsLocked {
		t.Fatal("expected conversation to open after inbound driver reply")
	}
	if conversation.WindowRemainingSeconds <= 0 {
		t.Fatalf("expected positive open window after reply, got %d", conversation.WindowRemainingSeconds)
	}
	var receivedLogged bool
	var openedLogged bool
	for _, command := range activityPublisher.commands {
		switch command.Action {
		case "whatsapp.message.received":
			if command.EntityType == "message" && command.ActorRole == "driver" {
				receivedLogged = true
			}
		case "whatsapp.conversation.window.opened":
			if command.EntityType == "conversation" && command.EntityID == conversationID {
				openedLogged = true
			}
		}
	}
	if !receivedLogged {
		t.Fatal("expected inbound whatsapp message activity to be published")
	}
	if !openedLogged {
		t.Fatal("expected conversation window opened activity to be published")
	}

	messagesReq := makeNotificationAuthRequest(http.MethodGet, "/notify/whatsapp/conversations/"+conversationID+"/messages", nil, "tenant-a")
	messagesRes := httptest.NewRecorder()
	handler.ServeHTTP(messagesRes, messagesReq)
	if messagesRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, messagesRes.Code, messagesRes.Body.String())
	}
	messagesEnvelope := decodeNotificationEnvelope(t, messagesRes)
	var messagesPayload whatsAppConversationMessagesPayload
	if err := json.Unmarshal(messagesEnvelope.Data, &messagesPayload); err != nil {
		t.Fatalf("decode messages payload: %v", err)
	}
	if len(messagesPayload.Messages) != 2 {
		t.Fatalf("expected 2 messages after inbound reply, got %d", len(messagesPayload.Messages))
	}
	if messagesPayload.Messages[1].Direction != "inbound" {
		t.Fatalf("expected inbound second message, got %q", messagesPayload.Messages[1].Direction)
	}

	textReq := makeNotificationAuthRequest(http.MethodPost, "/notify/whatsapp/send-message", []byte(`{"to":"+2348012345678","message":"Proceed to gate 2"}`), "tenant-a")
	textRes := httptest.NewRecorder()
	handler.ServeHTTP(textRes, textReq)
	if textRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, textRes.Code, textRes.Body.String())
	}
	if provider.lastTextContent != "Proceed to gate 2" {
		t.Fatalf("expected text message to be sent, got %q", provider.lastTextContent)
	}

	mediaReq := makeNotificationAuthRequest(http.MethodPost, "/notify/whatsapp/send-message-with-media", []byte(`{"to":"+2348012345678","media_type":"image","link":"https://example.com/job.png","caption":"Pickup map"}`), "tenant-a")
	mediaRes := httptest.NewRecorder()
	handler.ServeHTTP(mediaRes, mediaReq)
	if mediaRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, mediaRes.Code, mediaRes.Body.String())
	}
	if provider.lastMediaRequest.MediaType != "image" {
		t.Fatalf("expected image media request, got %#v", provider.lastMediaRequest)
	}

	messagesRes = httptest.NewRecorder()
	handler.ServeHTTP(messagesRes, messagesReq)
	messagesEnvelope = decodeNotificationEnvelope(t, messagesRes)
	if err := json.Unmarshal(messagesEnvelope.Data, &messagesPayload); err != nil {
		t.Fatalf("decode messages payload after sends: %v", err)
	}
	if len(messagesPayload.Messages) != 4 {
		t.Fatalf("expected 4 messages after text and media sends, got %d", len(messagesPayload.Messages))
	}
}

func TestWhatsAppWebhookKeywordReplyCreatesDriverStatusRequest(t *testing.T) {
	handler, _, provider, clock, _, driverStatusSender, _ := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")
	conversations := listNotificationConversations(t, handler, "tenant-a")
	conversationID := conversations[0].ID
	clock.now = clock.now.Add(2 * time.Minute)

	webhookPayload := fmt.Sprintf(`{
		"entry":[{
			"changes":[{
				"value":{
					"contacts":[{"profile":{"name":"Chukwuma Okafor"},"wa_id":"2348012345678"}],
					"messages":[{
						"id":"wamid.inbound.keyword.1",
						"from":"2348012345678",
						"timestamp":"%d",
						"type":"text",
						"context":{"id":"%s"},
						"text":{"body":"ENROUTE"}
					}]
				}
			}]
		}]
	}`,
		clock.now.Unix(),
		provider.lastTemplateResult.MessageID,
	)
	webhookReq := httptest.NewRequest(http.MethodPost, "/notify/whatsapp/webhook", strings.NewReader(webhookPayload))
	webhookReq.Header.Set("Content-Type", "application/json")
	webhookRes := httptest.NewRecorder()
	handler.ServeHTTP(webhookRes, webhookReq)
	if webhookRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, webhookRes.Code, webhookRes.Body.String())
	}

	if len(driverStatusSender.commands) != 1 {
		t.Fatalf("expected one driver status request command, got %d", len(driverStatusSender.commands))
	}
	command := driverStatusSender.commands[0]
	if command.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", command.TenantID)
	}
	if command.JobID != "job-1" {
		t.Fatalf("expected job-1, got %q", command.JobID)
	}
	if command.DriverID != "driver-1" {
		t.Fatalf("expected driver-1, got %q", command.DriverID)
	}
	if command.DriverPhone != "2348012345678" {
		t.Fatalf("expected normalized driver phone, got %q", command.DriverPhone)
	}
	if command.Keyword != "ENROUTE" {
		t.Fatalf("expected ENROUTE keyword, got %q", command.Keyword)
	}
	if command.SourceMessageID != "wamid.inbound.keyword.1" {
		t.Fatalf("expected source message id, got %q", command.SourceMessageID)
	}

	messagesReq := makeNotificationAuthRequest(http.MethodGet, "/notify/whatsapp/conversations/"+conversationID+"/messages", nil, "tenant-a")
	messagesRes := httptest.NewRecorder()
	handler.ServeHTTP(messagesRes, messagesReq)
	if messagesRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, messagesRes.Code, messagesRes.Body.String())
	}
	envelope := decodeNotificationEnvelope(t, messagesRes)
	var payload whatsAppConversationMessagesPayload
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		t.Fatalf("decode messages payload: %v", err)
	}
	inbound := payload.Messages[len(payload.Messages)-1]
	recorded, _ := inbound.Payload["driver_status_request_recorded"].(bool)
	if !recorded {
		t.Fatalf("expected inbound payload to mark driver status request as recorded, got %#v", inbound.Payload)
	}
	if got := fmt.Sprint(inbound.Payload["driver_status_request_id"]); got != "req-1" {
		t.Fatalf("expected request id req-1, got %q", got)
	}
	if got := fmt.Sprint(inbound.Payload["driver_status_requested"]); got != "en-route" {
		t.Fatalf("expected requested status en-route, got %q", got)
	}
}

func TestWhatsAppWebhookDeliveredStatusUpdatesOutboxAndConversationMessage(t *testing.T) {
	handler, _, provider, clock, outboxRepository, _, _ := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")

	conversations := listNotificationConversations(t, handler, "tenant-a")
	conversationID := conversations[0].ID
	clock.now = clock.now.Add(2 * time.Minute)
	webhookPayload := fmt.Sprintf(`{
		"entry":[{
			"changes":[{
				"value":{
					"statuses":[{
						"id":"%s",
						"status":"delivered",
						"timestamp":"%d",
						"recipient_id":"2348012345678"
					}]
				}
			}]
		}]
	}`,
		provider.lastTemplateResult.MessageID,
		clock.now.Unix(),
	)

	webhookReq := httptest.NewRequest(http.MethodPost, "/notify/whatsapp/webhook", strings.NewReader(webhookPayload))
	webhookReq.Header.Set("Content-Type", "application/json")
	webhookRes := httptest.NewRecorder()
	handler.ServeHTTP(webhookRes, webhookReq)
	if webhookRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, webhookRes.Code, webhookRes.Body.String())
	}

	outboxMessage, ok := outboxRepository.FindByProviderMessageID(provider.lastTemplateResult.MessageID)
	if !ok {
		t.Fatal("expected outbox message to be retrievable by provider message id")
	}
	if outboxMessage.Status != domain.StatusDelivered {
		t.Fatalf("expected outbox status %q, got %q", domain.StatusDelivered, outboxMessage.Status)
	}

	messagesReq := makeNotificationAuthRequest(http.MethodGet, "/notify/whatsapp/conversations/"+conversationID+"/messages", nil, "tenant-a")
	messagesRes := httptest.NewRecorder()
	handler.ServeHTTP(messagesRes, messagesReq)
	if messagesRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, messagesRes.Code, messagesRes.Body.String())
	}

	envelope := decodeNotificationEnvelope(t, messagesRes)
	var payload whatsAppConversationMessagesPayload
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		t.Fatalf("decode messages payload: %v", err)
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("expected 1 outbound template message, got %d", len(payload.Messages))
	}
	if payload.Messages[0].Status != domain.StatusDelivered {
		t.Fatalf("expected conversation message status %q, got %q", domain.StatusDelivered, payload.Messages[0].Status)
	}
}

func TestWhatsAppWebhookFailedStatusUpdatesOutboxAndConversationMessage(t *testing.T) {
	handler, _, provider, clock, outboxRepository, _, _ := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")

	conversations := listNotificationConversations(t, handler, "tenant-a")
	conversationID := conversations[0].ID
	clock.now = clock.now.Add(3 * time.Minute)
	webhookPayload := fmt.Sprintf(`{
		"entry":[{
			"changes":[{
				"value":{
					"statuses":[{
						"id":"%s",
						"status":"failed",
						"timestamp":"%d",
						"recipient_id":"2348012345678",
						"errors":[{"message":"recipient is unavailable"}]
					}]
				}
			}]
		}]
	}`,
		provider.lastTemplateResult.MessageID,
		clock.now.Unix(),
	)

	webhookReq := httptest.NewRequest(http.MethodPost, "/notify/whatsapp/webhook", strings.NewReader(webhookPayload))
	webhookReq.Header.Set("Content-Type", "application/json")
	webhookRes := httptest.NewRecorder()
	handler.ServeHTTP(webhookRes, webhookReq)
	if webhookRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, webhookRes.Code, webhookRes.Body.String())
	}

	outboxMessage, ok := outboxRepository.FindByProviderMessageID(provider.lastTemplateResult.MessageID)
	if !ok {
		t.Fatal("expected outbox message to be retrievable by provider message id")
	}
	if outboxMessage.Status != domain.StatusFailed {
		t.Fatalf("expected outbox status %q, got %q", domain.StatusFailed, outboxMessage.Status)
	}
	if provider.genericSendCount != 1 {
		t.Fatalf("expected one sms fallback send attempt, got %d", provider.genericSendCount)
	}
	if provider.lastGenericChannel != "sms" {
		t.Fatalf("expected sms fallback channel, got %q", provider.lastGenericChannel)
	}
	if provider.lastGenericRecipient != "+2348012345678" && provider.lastGenericRecipient != "2348012345678" {
		t.Fatalf("expected sms fallback recipient to match driver phone, got %q", provider.lastGenericRecipient)
	}
	if !strings.Contains(provider.lastGenericContent, "TM-2847") {
		t.Fatalf("expected sms fallback body to include job reference, got %q", provider.lastGenericContent)
	}

	messagesReq := makeNotificationAuthRequest(http.MethodGet, "/notify/whatsapp/conversations/"+conversationID+"/messages", nil, "tenant-a")
	messagesRes := httptest.NewRecorder()
	handler.ServeHTTP(messagesRes, messagesReq)
	if messagesRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, messagesRes.Code, messagesRes.Body.String())
	}

	envelope := decodeNotificationEnvelope(t, messagesRes)
	var payload whatsAppConversationMessagesPayload
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		t.Fatalf("decode messages payload: %v", err)
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("expected 1 outbound template message, got %d", len(payload.Messages))
	}
	if payload.Messages[0].Status != domain.StatusFailed {
		t.Fatalf("expected conversation message status %q, got %q", domain.StatusFailed, payload.Messages[0].Status)
	}
	if attempted, ok := payload.Messages[0].Payload["sms_fallback_attempted"].(bool); !ok || attempted == false {
		t.Fatalf("expected sms fallback attempted flag, got %#v", payload.Messages[0].Payload["sms_fallback_attempted"])
	}
	if status, ok := payload.Messages[0].Payload["sms_fallback_status"].(string); !ok || status != domain.StatusSent {
		t.Fatalf("expected sms fallback status %q, got %#v", domain.StatusSent, payload.Messages[0].Payload["sms_fallback_status"])
	}
	if messageID, ok := payload.Messages[0].Payload["sms_fallback_message_id"].(string); !ok || strings.TrimSpace(messageID) == "" {
		t.Fatalf("expected sms fallback outbox message id, got %#v", payload.Messages[0].Payload["sms_fallback_message_id"])
	}
}

func TestWhatsAppWebhookFailedStatusDoesNotDuplicateSMSFallback(t *testing.T) {
	handler, _, provider, clock, _, _, _ := notificationHandlerForTests(t)
	sendJobInstructionForTests(t, handler, "tenant-a")

	webhookPayload := fmt.Sprintf(`{
		"entry":[{
			"changes":[{
				"value":{
					"statuses":[{
						"id":"%s",
						"status":"failed",
						"timestamp":"%d",
						"recipient_id":"2348012345678",
						"errors":[{"message":"recipient is unavailable"}]
					}]
				}
			}]
		}]
	}`,
		provider.lastTemplateResult.MessageID,
		clock.now.Unix(),
	)

	for i := 0; i < 2; i++ {
		webhookReq := httptest.NewRequest(http.MethodPost, "/notify/whatsapp/webhook", strings.NewReader(webhookPayload))
		webhookReq.Header.Set("Content-Type", "application/json")
		webhookRes := httptest.NewRecorder()
		handler.ServeHTTP(webhookRes, webhookReq)
		if webhookRes.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s", http.StatusOK, webhookRes.Code, webhookRes.Body.String())
		}
	}

	if provider.genericSendCount != 1 {
		t.Fatalf("expected only one sms fallback attempt across repeated failed callbacks, got %d", provider.genericSendCount)
	}
}

func TestConversationEndpointsRequireDispatcherRole(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequestWithRole(http.MethodGet, "/notify/whatsapp/conversations", nil, "tenant-a", "FINANCE_VIEWER")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, res.Code, res.Body.String())
	}
	envelope := decodeNotificationEnvelope(t, res)
	if envelope.Message != "dispatcher role required" {
		t.Fatalf("expected dispatcher role error, got %#v", envelope)
	}
}

func TestVerifyWhatsAppWebhook(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := httptest.NewRequest(http.MethodGet, "/notify/whatsapp/webhook?hub.mode=subscribe&hub.verify_token=verify-token&hub.challenge=12345", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, res.Code, res.Body.String())
	}
	if body := strings.TrimSpace(res.Body.String()); body != "12345" {
		t.Fatalf("expected challenge echo, got %q", body)
	}
}

func TestNotificationDocsOpenAPIIncludesConversationEndpoints(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := httptest.NewRequest(http.MethodGet, "/docs/notification/openapi.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, fragment := range []string{"/notify/whatsapp/settings", "/notify/whatsapp/conversations", "/notify/whatsapp/send-message", "/notify/whatsapp/webhook", "WhatsAppAccountEnvelope"} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected openapi to contain %s", fragment)
		}
	}
}
