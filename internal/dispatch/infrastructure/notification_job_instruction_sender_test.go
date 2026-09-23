package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

func TestNotificationJobInstructionSenderSendJobInstruction(t *testing.T) {
	t.Parallel()

	type receivedRequest struct {
		Recipient        string `json:"recipient"`
		TemplateName     string `json:"templateName"`
		Language         string `json:"language"`
		DriverName       string `json:"driverName"`
		JobReference     string `json:"jobReference"`
		PickupLocation   string `json:"pickupLocation"`
		DeliveryLocation string `json:"deliveryLocation"`
		CargoDescription string `json:"cargoDescription"`
		Priority         string `json:"priority"`
	}

	var got receivedRequest
	server := httptest.NewServer(httpx.WithAuthContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notify/whatsapp/send-job-instruction" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		claims, ok := httpx.ClaimsFromRequest(r)
		if !ok || claims == nil {
			t.Fatalf("expected auth claims to be present")
		}
		if claims.TenantID != "tenant-1" {
			t.Fatalf("expected tenant tenant-1, got %q", claims.TenantID)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":{}}`))
	}), identity.SharedSecretVerifier("secret")))
	defer server.Close()

	sender := NewNotificationJobInstructionSender(server.URL, "secret", server.Client())

	err := sender.SendJobInstruction(context.Background(), application.DispatchJobInstructionPayload{
		TenantID:         "tenant-1",
		Recipient:        "+2348012345678",
		TemplateName:     "job_assignment_v1",
		Language:         "en_US",
		DriverName:       "Chukwuma Okafor",
		JobReference:     "TM-2847",
		PickupLocation:   "Apapa Port, Lagos",
		DeliveryLocation: "Ikeja Industrial Estate",
		CargoDescription: "Electronics, 50kg",
		Priority:         "urgent",
	})
	if err != nil {
		t.Fatalf("send job instruction: %v", err)
	}

	if got.TemplateName != "job_assignment_v1" {
		t.Fatalf("expected template name job_assignment_v1, got %q", got.TemplateName)
	}
	if got.JobReference != "TM-2847" {
		t.Fatalf("expected job reference TM-2847, got %q", got.JobReference)
	}
	if got.Recipient != "+2348012345678" {
		t.Fatalf("expected recipient, got %q", got.Recipient)
	}
}

func TestNotificationJobInstructionSenderSendJobInstructionError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"meta unavailable"}`))
	}))
	defer server.Close()

	sender := NewNotificationJobInstructionSender(server.URL, "secret", server.Client())
	err := sender.SendJobInstruction(context.Background(), application.DispatchJobInstructionPayload{
		TenantID:         "tenant-1",
		Recipient:        "+2348012345678",
		TemplateName:     "job_assignment_v1",
		DriverName:       "Chukwuma Okafor",
		JobReference:     "TM-2847",
		PickupLocation:   "Apapa Port, Lagos",
		DeliveryLocation: "Ikeja Industrial Estate",
		CargoDescription: "Electronics, 50kg",
		Priority:         "urgent",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); got != "notification service returned 502: meta unavailable" {
		t.Fatalf("unexpected error %q", got)
	}
}
