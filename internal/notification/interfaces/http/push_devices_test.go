package notificationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type memoryPushDevices struct{ devices map[string]domain.PushDevice }

func (m *memoryPushDevices) SaveDevice(_ context.Context, device domain.PushDevice) error {
	m.devices[device.Token] = device
	return nil
}

func (m *memoryPushDevices) DeleteUserDevice(_ context.Context, tenantID, userID, token string) error {
	if device, ok := m.devices[token]; ok && device.TenantID == tenantID && device.UserID == userID {
		delete(m.devices, token)
	}
	return nil
}

func (m *memoryPushDevices) DeleteToken(context.Context, string) error { return nil }
func (m *memoryPushDevices) TokensFor(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (m *memoryPushDevices) UsersWithDevices(context.Context, string) (map[string]bool, error) {
	return nil, nil
}

func TestPushDevicesBindTheTokenToTheSignedInUser(t *testing.T) {
	repository := &memoryPushDevices{devices: map[string]domain.PushDevice{}}
	mux := http.NewServeMux()
	(&Handler{}).WithPushDevices(application.NewPushDevicesUseCase(repository, inboxClock{time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)})).
		RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
	call := func(method, tenantID, body string) int {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, makeNotificationAuthRequest(method, "/notify/devices", []byte(body), tenantID))
		return recorder.Code
	}

	if code := call(http.MethodPut, "tenant-a", `{"token":"tok","platform":"ios","appVersion":"1.0.0"}`); code != http.StatusOK {
		t.Fatalf("register: %d", code)
	}
	if device := repository.devices["tok"]; device.UserID != "user-1" || device.TenantID != "tenant-a" || device.Platform != "ios" {
		t.Fatalf("device bound to %+v", device)
	}
	if code := call(http.MethodPut, "tenant-a", `{"token":"tok","platform":"ios","userId":"someone-else"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown field must be refused, got %d", code)
	}
	if code := call(http.MethodPut, "tenant-a", `{"token":"tok","platform":"web"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown platform must be refused, got %d", code)
	}
	if code := call(http.MethodPut, "", `{"token":"tok","platform":"ios"}`); code != http.StatusUnauthorized {
		t.Fatalf("anonymous register must be refused, got %d", code)
	}
	if code := call(http.MethodDelete, "tenant-b", `{"token":"tok"}`); code != http.StatusOK || len(repository.devices) != 1 {
		t.Fatalf("another tenant must not remove the device: %d", code)
	}
	if code := call(http.MethodDelete, "tenant-a", `{"token":"tok"}`); code != http.StatusOK || len(repository.devices) != 0 {
		t.Fatalf("unregister: %d %v", code, repository.devices)
	}
}
