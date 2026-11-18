package notificationhttp

import (
	"net/http"

	httpx "github.com/truckmoov/services-go/pkg/http"
)

const notificationSwaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Notification Service API Docs</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    const basePath = window.location.pathname.replace(/\/+$/, "");
    const specUrl = basePath + "/openapi.json";
    window.ui = SwaggerUIBundle({
      url: specUrl,
      dom_id: "#swagger-ui",
      deepLinking: true,
      displayRequestDuration: true,
      defaultModelsExpandDepth: 1
    });
  </script>
</body>
</html>`

func (h *Handler) docsUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	httpx.WriteHTML(w, notificationSwaggerUIHTML)
}

func (h *Handler) docsOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	httpx.WriteDocument(w, notificationOpenAPISpec())
}

func notificationOpenAPISpec() map[string]any {
	conversationIDParameter := map[string]any{
		"name":     "id",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string"},
	}
	messageIDParameter := map[string]any{
		"name":     "id",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string"},
	}
	phoneNumberIDParameter := map[string]any{
		"name":     "phoneNumberId",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string"},
	}
	return httpx.ApplyContract(map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Notification Service API",
			"version":     "0.1.0",
			"description": "Notification service API for outbound notifications, WhatsApp templates, and dispatcher-to-driver WhatsApp conversations.",
		},
		"servers": []map[string]any{{"url": "/"}},
		"tags": []map[string]any{{
			"name":        "notification",
			"description": "Notification service endpoints",
		}},
		"paths": map[string]any{
			"/notify/health": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Health check",
					"operationId": "notificationHealth",
					"responses": map[string]any{
						"200": openAPIJSONResponse("Service is healthy", map[string]any{"$ref": "#/components/schemas/HealthResponse"}),
					},
				},
			},
			"/notify/messages/send": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Queue and send a message",
					"operationId": "sendMessage",
					"requestBody": openAPIRequestBody("#/components/schemas/SendMessageRequest"),
					"responses": map[string]any{
						"202": openAPIJSONResponse("Message accepted", map[string]any{"$ref": "#/components/schemas/OutboxMessage"}),
						"502": openAPIJSONResponse("Provider failure", map[string]any{"$ref": "#/components/schemas/OutboxMessage"}),
					},
				},
			},
			"/notify/messages/retry/{id}": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Retry a failed message",
					"operationId": "retryMessage",
					"parameters":  []any{messageIDParameter},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Retry succeeded", map[string]any{"$ref": "#/components/schemas/OutboxMessage"}),
						"404": openAPIJSONResponse("Message not found", map[string]any{"$ref": "#/components/schemas/ErrorResponse"}),
						"409": openAPIJSONResponse("Only a failed message can be retried", map[string]any{"$ref": "#/components/schemas/ErrorResponse"}),
						"502": openAPIJSONResponse("Provider failure", map[string]any{"$ref": "#/components/schemas/OutboxMessage"}),
					},
				},
			},
			"/notify/whatsapp/settings": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Connect a WhatsApp account through embedded signup",
					"operationId": "connectWhatsAppAccount",
					"requestBody": openAPIRequestBody("#/components/schemas/ConnectWhatsAppSettingsRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("WhatsApp account connected", map[string]any{"$ref": "#/components/schemas/WhatsAppAccountEnvelope"}),
						"400": openAPIJSONResponse("Invalid embedded signup payload", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Administrator role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Get the connected WhatsApp account for the tenant",
					"operationId": "getWhatsAppAccount",
					"responses": map[string]any{
						"200": openAPIJSONResponse("Connected WhatsApp account", map[string]any{"$ref": "#/components/schemas/WhatsAppAccountEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Administrator role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("WhatsApp account not connected", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Failed to load WhatsApp account", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/settings/{phoneNumberId}": map[string]any{
				"delete": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Disconnect a connected WhatsApp account",
					"operationId": "disconnectWhatsAppAccount",
					"parameters":  []any{phoneNumberIDParameter},
					"responses": map[string]any{
						"200": openAPIJSONResponse("WhatsApp account disconnected", map[string]any{"$ref": "#/components/schemas/WhatsAppDisconnectEnvelope"}),
						"400": openAPIJSONResponse("Invalid request", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Administrator role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("WhatsApp account not connected", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/create-template": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Create WhatsApp template in Meta",
					"operationId": "createWhatsAppTemplate",
					"requestBody": openAPIRequestBody("#/components/schemas/CreateWhatsAppTemplateRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Template submitted", map[string]any{"$ref": "#/components/schemas/WhatsAppTemplateCreateEnvelope"}),
						"400": openAPIJSONResponse("Validation or Meta request error", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/get-all-templates": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Fetch WhatsApp templates from Meta",
					"operationId": "getAllWhatsAppTemplates",
					"responses": map[string]any{
						"200": openAPIJSONResponse("Template list", map[string]any{"$ref": "#/components/schemas/WhatsAppTemplateListEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Meta fetch failed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/send-other-template": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Send an approved non-job-assignment WhatsApp template",
					"operationId": "sendOtherTemplate",
					"requestBody": openAPIRequestBody("#/components/schemas/SendOtherTemplateRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Template sent", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Template not found", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
						"502": openAPIJSONResponse("Delivery failed", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/send-job-instruction": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Send job instruction via approved WhatsApp template",
					"operationId": "sendJobInstruction",
					"requestBody": openAPIRequestBody("#/components/schemas/SendJobInstructionRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Job instruction sent", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Template not found", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
						"502": openAPIJSONResponse("Delivery failed", map[string]any{"$ref": "#/components/schemas/OutboxMessageEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/send-message": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Send a normal WhatsApp text message in an open conversation",
					"operationId": "sendWhatsAppTextMessage",
					"requestBody": openAPIRequestBody("#/components/schemas/SendWhatsAppTextMessageRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Message sent", map[string]any{"$ref": "#/components/schemas/WhatsAppConversationSendEnvelope"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Conversation not found", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"409": openAPIJSONResponse("Conversation locked", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Delivery failed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/send-message-with-media": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Send a normal WhatsApp media message in an open conversation",
					"operationId": "sendWhatsAppMediaMessage",
					"requestBody": openAPIRequestBody("#/components/schemas/SendWhatsAppMediaMessageRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Media message sent", map[string]any{"$ref": "#/components/schemas/WhatsAppConversationSendEnvelope"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Conversation not found", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"409": openAPIJSONResponse("Conversation locked", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Delivery failed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/delivery-log": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "List the delivery log for the authenticated tenant",
					"description": "Answers \"did we tell them\". The tenant comes from the token, never from a parameter.",
					"operationId": "listDeliveryLog",
					"parameters": []any{
						map[string]any{"name": "notification_type", "in": "query", "required": false, "schema": map[string]any{"type": "string", "enum": []string{"generic", "kyc_decision", "compliance_reminder", "job_assignment", "campaign", "spending_limit", "job_assigned", "job_updated", "job_reminder", "job_cancelled", "issue_resolved", "device_verified", "device_verification_failed"}}},
						map[string]any{"name": "channel", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						map[string]any{"name": "status", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						map[string]any{"name": "limit", "in": "query", "required": false, "description": "Defaults to 50, capped at 200.", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}},
						map[string]any{"name": "offset", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 0}},
					},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Delivery log page", map[string]any{"$ref": "#/components/schemas/DeliveryLogPageEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/devices": map[string]any{
				"put": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Register this app install for push notifications",
					"description": "The user and tenant come from the token. A token already registered is re-bound to the caller.",
					"operationId": "putPushDevice",
					"requestBody": openAPIRequestBody("#/components/schemas/PushDeviceRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Registered", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
				"delete": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Stop push notifications to this app install",
					"description": "Removes the token only while it belongs to the caller.",
					"operationId": "deletePushDevice",
					"requestBody": openAPIRequestBody("#/components/schemas/PushDeviceDeleteRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Removed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/preferences": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Read how the authenticated tenant is told about job offers",
					"description": "The tenant comes from the token. Until a choice is saved, email is on and WhatsApp and SMS are off.",
					"operationId": "getNotificationPreferences",
					"responses": map[string]any{
						"200": openAPIJSONResponse("Notification choices", map[string]any{"$ref": "#/components/schemas/NotificationPreferences"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
				"put": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Choose how the authenticated tenant is told about job offers",
					"description": "WhatsApp is sent from the tenant's own number and falls back to SMS when it cannot be delivered. At least one channel must stay on.",
					"operationId": "putNotificationPreferences",
					"requestBody": openAPIRequestBody("#/components/schemas/NotificationPreferencesRequest"),
					"responses": map[string]any{
						"200": openAPIJSONResponse("Saved choices", map[string]any{"$ref": "#/components/schemas/NotificationPreferences"}),
						"400": openAPIJSONResponse("Validation error", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/system/dead-letters": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "List events the bus gave up delivering (platform staff only)",
					"description": "Kept for 30 days. The event itself is not listed.",
					"operationId": "listDeadLetters",
					"parameters": []any{
						map[string]any{"name": "page", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1}},
					},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Dead letter page", map[string]any{"$ref": "#/components/schemas/DeadLetterPage"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Platform messaging permission required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/system/dead-letters/{id}/replay": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Publish a dead letter's original event again, once (platform staff only)",
					"operationId": "replayDeadLetter",
					"parameters": []any{
						map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Replayed", map[string]any{"$ref": "#/components/schemas/DeadLetter"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Platform messaging permission required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Dead letter not found", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"409": openAPIJSONResponse("Already replayed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"422": openAPIJSONResponse("Cannot be replayed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Publish failed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/conversations": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "List WhatsApp conversations for dispatcher-capable roles",
					"operationId": "listWhatsAppConversations",
					"parameters": []any{
						map[string]any{"name": "query", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						map[string]any{"name": "state", "in": "query", "required": false, "schema": map[string]any{"type": "string", "enum": []string{"open", "locked"}}},
					},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Conversation list", map[string]any{"$ref": "#/components/schemas/WhatsAppConversationListEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/conversations/{id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Get a WhatsApp conversation",
					"operationId": "getWhatsAppConversation",
					"parameters":  []any{conversationIDParameter},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Conversation details", map[string]any{"$ref": "#/components/schemas/WhatsAppConversationEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Conversation not found", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/conversations/{id}/messages": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "List messages for a WhatsApp conversation",
					"operationId": "listWhatsAppConversationMessages",
					"parameters":  []any{conversationIDParameter},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Conversation messages", map[string]any{"$ref": "#/components/schemas/WhatsAppConversationMessagesEnvelope"}),
						"401": openAPIJSONResponse("Authentication required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Dispatcher role required", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"404": openAPIJSONResponse("Conversation not found", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Repository failure", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/sms/webhook": map[string]any{
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Receive Dotgo SMS delivery receipts",
					"description": "Called by Dotgo, not by clients. The callback address given to Dotgo carries a shared secret in the token query parameter, because Dotgo does not sign its callbacks. A receipt is matched to a sent message by the request id it was sent under; one that matches nothing is acknowledged and ignored.",
					"operationId": "handleSMSDeliveryReceipt",
					"parameters": []any{
						map[string]any{"name": "token", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string", "enum": []string{"sent", "delivered", "undelivered", "failed", "rejected"}}, "error_reason": map[string]any{"type": "string"}},
					}}}},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Receipt acknowledged", map[string]any{"$ref": "#/components/schemas/WebhookAckEnvelope"}),
						"400": openAPIJSONResponse("Invalid request body", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"401": openAPIJSONResponse("Missing or wrong token", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Receipt processing failed; Dotgo should retry", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"503": openAPIJSONResponse("Delivery receipts are not configured", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
			"/notify/whatsapp/webhook": map[string]any{
				"get": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Verify the WhatsApp webhook",
					"operationId": "verifyWhatsAppWebhook",
					"parameters": []any{
						map[string]any{"name": "hub.mode", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
						map[string]any{"name": "hub.verify_token", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
						map[string]any{"name": "hub.challenge", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Webhook verified", "content": map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}}},
						"400": openAPIJSONResponse("Invalid verification request", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"403": openAPIJSONResponse("Invalid verification token", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"500": openAPIJSONResponse("Webhook token not configured", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
				"post": map[string]any{
					"tags":        []string{"notification"},
					"summary":     "Receive inbound WhatsApp webhook events",
					"operationId": "handleWhatsAppWebhook",
					"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": true}}}},
					"responses": map[string]any{
						"200": openAPIJSONResponse("Webhook processed", map[string]any{"$ref": "#/components/schemas/WebhookAckEnvelope"}),
						"400": openAPIJSONResponse("Invalid request body", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
						"502": openAPIJSONResponse("Webhook processing failed", map[string]any{"$ref": "#/components/schemas/NotificationEnvelope"}),
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"HealthResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "string"},
						"service": map[string]any{"type": "string"},
					},
				},
				"SendMessageRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tenant_id": map[string]any{"type": "string"},
						"channel":   map[string]any{"type": "string"},
						"recipient": map[string]any{"type": "string"},
						"content":   map[string]any{"type": "string"},
					},
					"required": []string{"tenant_id", "channel", "recipient", "content"},
				},
				"OutboxMessage": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"tenant_id":  map[string]any{"type": "string"},
						"channel":    map[string]any{"type": "string"},
						"recipient":  map[string]any{"type": "string"},
						"content":    map[string]any{"type": "string"},
						"status":     map[string]any{"type": "string"},
						"retries":    map[string]any{"type": "integer"},
						"created_at": map[string]any{"type": "string", "format": "date-time"},
						"updated_at": map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"NotificationEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"nullable": true},
					},
				},
				"OutboxMessageEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/OutboxMessage"},
					},
				},
				"ConnectWhatsAppSettingsRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"phone_number_id":     map[string]any{"type": "string"},
						"coexistence":         map[string]any{"type": "boolean"},
						"phoneNumberId":       map[string]any{"type": "string"},
						"business_id":         map[string]any{"type": "string"},
						"businessId":          map[string]any{"type": "string"},
						"business_account_id": map[string]any{"type": "string"},
						"businessAccountId":   map[string]any{"type": "string"},
						"code":                map[string]any{"type": "string"},
					},
					"required": []string{"phone_number_id", "business_account_id", "code"},
				},
				"WhatsAppAccount": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                   map[string]any{"type": "string"},
						"tenant_id":            map[string]any{"type": "string"},
						"phone_number_id":      map[string]any{"type": "string"},
						"business_account_id":  map[string]any{"type": "string"},
						"display_phone_number": map[string]any{"type": "string"},
						"verified_name":        map[string]any{"type": "string"},
						"business_name":        map[string]any{"type": "string"},
						"quality_rating":       map[string]any{"type": "string"},
						"number_status":        map[string]any{"type": "string"},
						"name_status":          map[string]any{"type": "string"},
						"messaging_limit_tier": map[string]any{"type": "string"},
						"platform_type":        map[string]any{"type": "string"},
						"coexistence":          map[string]any{"type": "boolean"},
						"token_status":         map[string]any{"type": "string", "enum": []string{"valid", "needs_reconnect"}},
						"token_expires_at":     map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"token_never_expires":  map[string]any{"type": "boolean"},
						"status_error":         map[string]any{"type": "string"},
						"webhook_error":        map[string]any{"type": "string"},
						"register_error":       map[string]any{"type": "string"},
						"registered_at":        map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"last_checked_at":      map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"steps": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
							"name":   map[string]any{"type": "string"},
							"status": map[string]any{"type": "string", "enum": []string{"ok", "failed", "skipped"}},
							"error":  map[string]any{"type": "string"},
						}}},
						"webhook_subscribed_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"connected_at":          map[string]any{"type": "string", "format": "date-time"},
						"created_at":            map[string]any{"type": "string", "format": "date-time"},
						"updated_at":            map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"WhatsAppAccountEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppAccount"},
					},
				},
				"WhatsAppDisconnectPayload": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"phone_number_id": map[string]any{"type": "string"},
						"unsubscribed":    map[string]any{"type": "boolean"},
						"deregistered":    map[string]any{"type": "boolean"},
						"warnings":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
				},
				"WhatsAppDisconnectEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppDisconnectPayload"},
					},
				},
				"MetaComponent": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
				"CreateWhatsAppTemplateRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":                  map[string]any{"type": "string"},
						"template_type":         map[string]any{"type": "string", "enum": []string{"job_assignment", "other"}},
						"templateType":          map[string]any{"type": "string", "enum": []string{"job_assignment", "other"}},
						"header":                map[string]any{"type": "string"},
						"body":                  map[string]any{"type": "string"},
						"language":              map[string]any{"type": "string"},
						"category":              map[string]any{"type": "string"},
						"allow_category_change": map[string]any{"type": "boolean"},
						"allowCategoryChange":   map[string]any{"type": "boolean"},
						"parameter_format":      map[string]any{"type": "string"},
						"parameterFormat":       map[string]any{"type": "string"},
						"components": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/MetaComponent"},
						},
					},
					"required": []string{"name"},
				},
				"WhatsAppTemplateCreateResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":       map[string]any{"type": "string"},
						"status":   map[string]any{"type": "string"},
						"category": map[string]any{"type": "string"},
					},
				},
				"WhatsAppTemplateCreateEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppTemplateCreateResponse"},
					},
				},
				"WhatsAppTemplate": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                map[string]any{"type": "string"},
						"name":              map[string]any{"type": "string"},
						"language":          map[string]any{"type": "string"},
						"status":            map[string]any{"type": "string"},
						"category":          map[string]any{"type": "string"},
						"sub_category":      map[string]any{"type": "string"},
						"previous_category": map[string]any{"type": "string"},
						"rejected_reason":   map[string]any{"type": "string"},
						"components": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/MetaComponent"},
						},
					},
				},
				"WhatsAppTemplatePaging": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cursors": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
						"next":    map[string]any{"type": "string"},
					},
				},
				"WhatsAppTemplateListResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"data":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/WhatsAppTemplate"}},
						"paging": map[string]any{"$ref": "#/components/schemas/WhatsAppTemplatePaging"},
					},
				},
				"WhatsAppTemplateListEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppTemplateListResponse"},
					},
				},
				"SendJobInstructionHeaderMedia": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":     map[string]any{"type": "string", "enum": []string{"image", "video", "document"}},
						"link":     map[string]any{"type": "string"},
						"id":       map[string]any{"type": "string"},
						"filename": map[string]any{"type": "string"},
					},
				},
				"SendJobInstructionButtonParameter": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index":   map[string]any{"type": "integer"},
						"type":    map[string]any{"type": "string", "enum": []string{"quick_reply", "url", "copy_code"}},
						"text":    map[string]any{"type": "string"},
						"payload": map[string]any{"type": "string"},
					},
					"required": []string{"index"},
				},
				"TemplateVariables": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"SendOtherTemplateRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"to":                 map[string]any{"type": "string"},
						"recipient":          map[string]any{"type": "string"},
						"template_name":      map[string]any{"type": "string"},
						"templateName":       map[string]any{"type": "string"},
						"language":           map[string]any{"type": "string"},
						"variables":          map[string]any{"$ref": "#/components/schemas/TemplateVariables"},
						"template_variables": map[string]any{"$ref": "#/components/schemas/TemplateVariables"},
						"templateVariables":  map[string]any{"$ref": "#/components/schemas/TemplateVariables"},
						"header_media":       map[string]any{"$ref": "#/components/schemas/SendJobInstructionHeaderMedia"},
						"headerMedia":        map[string]any{"$ref": "#/components/schemas/SendJobInstructionHeaderMedia"},
						"button_parameters":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SendJobInstructionButtonParameter"}},
						"buttonParameters":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SendJobInstructionButtonParameter"}},
					},
					"required": []string{"to", "template_name"},
				},
				"SendJobInstructionRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"to":                map[string]any{"type": "string"},
						"recipient":         map[string]any{"type": "string"},
						"template_name":     map[string]any{"type": "string"},
						"templateName":      map[string]any{"type": "string"},
						"language":          map[string]any{"type": "string"},
						"job_id":            map[string]any{"type": "string", "description": "Dispatch job id used to correlate subsequent driver keyword replies back to the job."},
						"jobId":             map[string]any{"type": "string", "description": "CamelCase alias for job_id."},
						"driver_id":         map[string]any{"type": "string", "description": "Assigned driver id used to validate later status reports."},
						"driverId":          map[string]any{"type": "string", "description": "CamelCase alias for driver_id."},
						"driver_name":       map[string]any{"type": "string"},
						"driverName":        map[string]any{"type": "string"},
						"job_reference":     map[string]any{"type": "string"},
						"jobReference":      map[string]any{"type": "string"},
						"pickup_location":   map[string]any{"type": "string"},
						"pickupLocation":    map[string]any{"type": "string"},
						"delivery_location": map[string]any{"type": "string"},
						"deliveryLocation":  map[string]any{"type": "string"},
						"cargo_description": map[string]any{"type": "string"},
						"cargoDescription":  map[string]any{"type": "string"},
						"priority":          map[string]any{"type": "string", "enum": []string{"low", "normal", "high", "urgent"}},
						"header_media":      map[string]any{"$ref": "#/components/schemas/SendJobInstructionHeaderMedia"},
						"headerMedia":       map[string]any{"$ref": "#/components/schemas/SendJobInstructionHeaderMedia"},
						"button_parameters": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SendJobInstructionButtonParameter"}},
						"buttonParameters":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SendJobInstructionButtonParameter"}},
					},
					"required": []string{"to", "template_name", "driver_name", "job_reference", "pickup_location", "delivery_location", "cargo_description", "priority"},
				},
				"SendWhatsAppTextMessageRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"to":        map[string]any{"type": "string"},
						"recipient": map[string]any{"type": "string"},
						"message":   map[string]any{"type": "string"},
						"content":   map[string]any{"type": "string"},
					},
					"required": []string{"to", "message"},
				},
				"SendWhatsAppMediaMessageRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"to":         map[string]any{"type": "string"},
						"recipient":  map[string]any{"type": "string"},
						"media_type": map[string]any{"type": "string", "enum": []string{"image", "video", "audio", "document"}},
						"mediaType":  map[string]any{"type": "string", "enum": []string{"image", "video", "audio", "document"}},
						"media_id":   map[string]any{"type": "string"},
						"mediaId":    map[string]any{"type": "string"},
						"link":       map[string]any{"type": "string"},
						"media_link": map[string]any{"type": "string"},
						"mediaLink":  map[string]any{"type": "string"},
						"caption":    map[string]any{"type": "string"},
						"filename":   map[string]any{"type": "string"},
					},
					"required": []string{"to", "media_type"},
				},
				"WhatsAppConversation": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                       map[string]any{"type": "string"},
						"tenant_id":                map[string]any{"type": "string"},
						"driver_phone":             map[string]any{"type": "string"},
						"driver_name":              map[string]any{"type": "string"},
						"last_inbound_message_at":  map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"last_outbound_message_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"last_template_sent_at":    map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"window_expires_at":        map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"window_remaining_seconds": map[string]any{"type": "integer"},
						"window_remaining_hours":   map[string]any{"type": "number", "format": "double"},
						"is_locked":                map[string]any{"type": "boolean"},
						"last_message_at":          map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"last_message_preview":     map[string]any{"type": "string"},
						"last_message_direction":   map[string]any{"type": "string"},
						"last_message_type":        map[string]any{"type": "string"},
						"created_at":               map[string]any{"type": "string", "format": "date-time"},
						"updated_at":               map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"DeliveryLogEntry": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                  map[string]any{"type": "string"},
						"tenant_id":           map[string]any{"type": "string"},
						"notification_type":   map[string]any{"type": "string"},
						"channel":             map[string]any{"type": "string"},
						"recipient":           map[string]any{"type": "string"},
						"subject":             map[string]any{"type": "string"},
						"content":             map[string]any{"type": "string", "description": "The message as it was sent, not as the template reads now."},
						"status":              map[string]any{"type": "string"},
						"provider_message_id": map[string]any{"type": "string"},
						"failure_reason":      map[string]any{"type": "string"},
						"created_at":          map[string]any{"type": "string", "format": "date-time"},
						"updated_at":          map[string]any{"type": "string", "format": "date-time"},
						"delivered_at":        map[string]any{"type": "string", "format": "date-time", "nullable": true},
					},
				},
				"DeliveryLogPage": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"entries": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/DeliveryLogEntry"}},
						"total":   map[string]any{"type": "integer", "description": "Entries matching the filters, not the page."},
						"limit":   map[string]any{"type": "integer"},
						"offset":  map[string]any{"type": "integer"},
					},
				},
				"NotificationPreferences": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tenant_id":  map[string]any{"type": "string"},
						"email":      map[string]any{"type": "boolean"},
						"whatsapp":   map[string]any{"type": "boolean"},
						"sms":        map[string]any{"type": "boolean"},
						"updated_at": map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"PushDeviceRequest": map[string]any{
					"type":     "object",
					"required": []string{"token", "platform"},
					"properties": map[string]any{
						"token":      map[string]any{"type": "string"},
						"platform":   map[string]any{"type": "string", "enum": []string{"ios", "android"}},
						"appVersion": map[string]any{"type": "string"},
					},
				},
				"PushDeviceDeleteRequest": map[string]any{
					"type":     "object",
					"required": []string{"token"},
					"properties": map[string]any{
						"token": map[string]any{"type": "string"},
					},
				},
				"NotificationPreferencesRequest": map[string]any{
					"type":     "object",
					"required": []string{"email", "whatsapp", "sms"},
					"properties": map[string]any{
						"email":    map[string]any{"type": "boolean"},
						"whatsapp": map[string]any{"type": "boolean"},
						"sms":      map[string]any{"type": "boolean"},
					},
				},
				"DeadLetter": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":               map[string]any{"type": "string"},
						"original_subject": map[string]any{"type": "string"},
						"tenant_id":        map[string]any{"type": "string", "description": "The tenant of the event that failed."},
						"event_type":       map[string]any{"type": "string"},
						"attempts":         map[string]any{"type": "integer"},
						"last_error":       map[string]any{"type": "string"},
						"failed_at":        map[string]any{"type": "string", "format": "date-time"},
						"replayed_at":      map[string]any{"type": "string", "format": "date-time", "nullable": true},
					},
				},
				"DeadLetterPage": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/DeadLetter"}},
						"total": map[string]any{"type": "integer"},
						"page":  map[string]any{"type": "integer"},
					},
				},
				"DeliveryLogPageEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/DeliveryLogPage"},
					},
				},
				"WhatsAppConversationListEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/WhatsAppConversation"}},
					},
				},
				"WhatsAppConversationEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppConversation"},
					},
				},
				"WhatsAppConversationMessage": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                  map[string]any{"type": "string"},
						"tenant_id":           map[string]any{"type": "string"},
						"conversation_id":     map[string]any{"type": "string"},
						"provider_message_id": map[string]any{"type": "string"},
						"direction":           map[string]any{"type": "string"},
						"message_type":        map[string]any{"type": "string"},
						"status":              map[string]any{"type": "string"},
						"sender_role":         map[string]any{"type": "string"},
						"sender_subject":      map[string]any{"type": "string"},
						"from_phone":          map[string]any{"type": "string"},
						"to_phone":            map[string]any{"type": "string"},
						"text":                map[string]any{"type": "string"},
						"template_name":       map[string]any{"type": "string"},
						"media_type":          map[string]any{"type": "string"},
						"media_id":            map[string]any{"type": "string"},
						"media_link":          map[string]any{"type": "string"},
						"caption":             map[string]any{"type": "string"},
						"payload":             map[string]any{"type": "object", "additionalProperties": true},
						"created_at":          map[string]any{"type": "string", "format": "date-time"},
						"updated_at":          map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"WhatsAppConversationMessagesPayload": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"conversation": map[string]any{"$ref": "#/components/schemas/WhatsAppConversation"},
						"messages":     map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/WhatsAppConversationMessage"}},
					},
				},
				"WhatsAppConversationMessagesEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppConversationMessagesPayload"},
					},
				},
				"WhatsAppConversationSendPayload": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"outbox_message": map[string]any{"$ref": "#/components/schemas/OutboxMessage"},
						"conversation":   map[string]any{"$ref": "#/components/schemas/WhatsAppConversation"},
						"message":        map[string]any{"$ref": "#/components/schemas/WhatsAppConversationMessage"},
					},
				},
				"WhatsAppConversationSendEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WhatsAppConversationSendPayload"},
					},
				},
				"WebhookAck": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"processed": map[string]any{"type": "integer"},
					},
				},
				"WebhookAckEnvelope": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/WebhookAck"},
					},
				},
			},
		},
	})
}

func openAPIJSONResponse(description string, schema any) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

func openAPIRequestBody(schemaRef string) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": schemaRef},
			},
		},
	}
}
