package notificationhttp

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type deliveryLogEntryResponse struct {
	ID                string  `json:"id"`
	TenantID          string  `json:"tenant_id"`
	NotificationType  string  `json:"notification_type"`
	Channel           string  `json:"channel"`
	Recipient         string  `json:"recipient"`
	Subject           string  `json:"subject,omitempty"`
	Content           string  `json:"content"`
	Status            string  `json:"status"`
	ProviderMessageID string  `json:"provider_message_id,omitempty"`
	FailureReason     string  `json:"failure_reason,omitempty"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	DeliveredAt       *string `json:"delivered_at,omitempty"`
}

type deliveryLogPageResponse struct {
	Entries []deliveryLogEntryResponse `json:"entries"`
	Total   int                        `json:"total"`
	Limit   int                        `json:"limit"`
	Offset  int                        `json:"offset"`
}

// listDeliveryLog answers "did we tell them" for one tenant. The tenant comes
// from the verified token, never from a query parameter, so a caller cannot
// read another tenant's log by asking for it.
func (h *Handler) listDeliveryLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	query := r.URL.Query()
	page, err := h.listDeliveryLogUseCase.Execute(r.Context(), tenantID, domain.DeliveryLogFilters{
		NotificationType: strings.TrimSpace(query.Get("notification_type")),
		Channel:          strings.TrimSpace(query.Get("channel")),
		Status:           strings.TrimSpace(query.Get("status")),
		Limit:            parsePositiveInt(query.Get("limit")),
		Offset:           parsePositiveInt(query.Get("offset")),
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", buildDeliveryLogPageResponse(page))
}

func buildDeliveryLogPageResponse(page application.DeliveryLogPage) deliveryLogPageResponse {
	entries := make([]deliveryLogEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entries = append(entries, buildDeliveryLogEntryResponse(entry))
	}
	return deliveryLogPageResponse{
		Entries: entries,
		Total:   page.Total,
		Limit:   page.Limit,
		Offset:  page.Offset,
	}
}

func buildDeliveryLogEntryResponse(entry domain.DeliveryLogEntry) deliveryLogEntryResponse {
	response := deliveryLogEntryResponse{
		ID:                entry.ID,
		TenantID:          entry.TenantID,
		NotificationType:  entry.NotificationType,
		Channel:           entry.Channel,
		Recipient:         entry.Recipient,
		Subject:           entry.Subject,
		Content:           entry.Content,
		Status:            entry.Status,
		ProviderMessageID: entry.ProviderMessageID,
		FailureReason:     entry.FailureReason,
		CreatedAt:         entry.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:         entry.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if entry.DeliveredAt != nil {
		delivered := entry.DeliveredAt.UTC().Format(time.RFC3339)
		response.DeliveredAt = &delivered
	}
	return response
}

// parsePositiveInt treats anything unparseable as absent. The use case clamps
// the result, so a bad value falls back to the default page rather than 400.
func parsePositiveInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}
