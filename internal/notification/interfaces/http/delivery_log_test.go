package notificationhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sendOneMessage(t *testing.T, handler http.Handler, tenantID, recipient string) {
	t.Helper()
	body := fmt.Sprintf(`{"tenant_id":%q,"channel":"sms","recipient":%q,"content":"hello"}`, tenantID, recipient)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/messages/send", []byte(body), tenantID)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted && res.Code != http.StatusOK && res.Code != http.StatusCreated {
		t.Fatalf("send failed with %d: %s", res.Code, res.Body.String())
	}
}

func readDeliveryLog(t *testing.T, handler http.Handler, tenantID, query string) deliveryLogPageResponse {
	t.Helper()
	req := makeNotificationAuthRequest(http.MethodGet, "/notify/delivery-log"+query, nil, tenantID)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	envelope := decodeNotificationEnvelope(t, res)
	var page deliveryLogPageResponse
	if err := json.Unmarshal(envelope.Data, &page); err != nil {
		t.Fatalf("decode delivery log page: %v", err)
	}
	return page
}

func TestDeliveryLogReturnsWhatWasSent(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	sendOneMessage(t, handler, "tenant-a", "+2348000000001")

	page := readDeliveryLog(t, handler, "tenant-a", "")

	if page.Total != 1 || len(page.Entries) != 1 {
		t.Fatalf("expected one entry, got total=%d len=%d", page.Total, len(page.Entries))
	}
	entry := page.Entries[0]
	if entry.Recipient != "+2348000000001" {
		t.Fatalf("expected the recipient we sent to, got %q", entry.Recipient)
	}
	if entry.Content != "hello" {
		t.Fatalf("expected the message as sent, got %q", entry.Content)
	}
	if entry.Channel != "sms" {
		t.Fatalf("expected channel sms, got %q", entry.Channel)
	}
}

func TestDeliveryLogIsScopedToTheAuthenticatedTenant(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	sendOneMessage(t, handler, "tenant-a", "+2348000000001")
	sendOneMessage(t, handler, "tenant-b", "+2348000000002")

	page := readDeliveryLog(t, handler, "tenant-a", "")

	if page.Total != 1 {
		t.Fatalf("expected only this tenant's entry, got total=%d", page.Total)
	}
	if page.Entries[0].TenantID != "tenant-a" {
		t.Fatalf("leaked another tenant's entry: %q", page.Entries[0].TenantID)
	}
}

func TestDeliveryLogTenantCannotBeOverriddenByQuery(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	sendOneMessage(t, handler, "tenant-b", "+2348000000002")

	page := readDeliveryLog(t, handler, "tenant-a", "?tenant_id=tenant-b")

	if page.Total != 0 {
		t.Fatalf("query parameter overrode the token's tenant, got total=%d", page.Total)
	}
}

func TestDeliveryLogFiltersByChannel(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	sendOneMessage(t, handler, "tenant-a", "+2348000000001")

	matching := readDeliveryLog(t, handler, "tenant-a", "?channel=sms")
	if matching.Total != 1 {
		t.Fatalf("expected the sms entry, got total=%d", matching.Total)
	}

	other := readDeliveryLog(t, handler, "tenant-a", "?channel=whatsapp")
	if other.Total != 0 {
		t.Fatalf("expected no whatsapp entries, got total=%d", other.Total)
	}
}

func TestDeliveryLogPagesWithoutLosingTheTotal(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	for i := 0; i < 3; i++ {
		sendOneMessage(t, handler, "tenant-a", fmt.Sprintf("+23480000000%02d", i))
	}

	page := readDeliveryLog(t, handler, "tenant-a", "?limit=2")

	if len(page.Entries) != 2 {
		t.Fatalf("expected a page of 2, got %d", len(page.Entries))
	}
	if page.Total != 3 {
		t.Fatalf("expected the total to count every match, got %d", page.Total)
	}

	second := readDeliveryLog(t, handler, "tenant-a", "?limit=2&offset=2")
	if len(second.Entries) != 1 {
		t.Fatalf("expected the remaining entry, got %d", len(second.Entries))
	}
}

func TestDeliveryLogClampsAnOversizedLimit(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	sendOneMessage(t, handler, "tenant-a", "+2348000000001")

	page := readDeliveryLog(t, handler, "tenant-a", "?limit=100000")

	if page.Limit > 200 {
		t.Fatalf("limit was not clamped, got %d", page.Limit)
	}
}

func TestDeliveryLogRequiresAuth(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequest(http.MethodGet, "/notify/delivery-log", nil, "")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", res.Code, res.Body.String())
	}
}

func TestDeliveryLogRejectsAnInsufficientRole(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequestWithRole(http.MethodGet, "/notify/delivery-log", nil, "tenant-a", "DRIVER")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", res.Code, res.Body.String())
	}
}

func TestDeliveryLogRejectsANonGetMethod(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)
	req := makeNotificationAuthRequest(http.MethodPost, "/notify/delivery-log", []byte(`{}`), "tenant-a")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", res.Code, res.Body.String())
	}
}
