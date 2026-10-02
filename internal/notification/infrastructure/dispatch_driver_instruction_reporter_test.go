package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

func TestDispatchDriverInstructionReporterPostsTheOutcome(t *testing.T) {
	t.Parallel()

	var got driverInstructionOutcomeBody
	server := httptest.NewServer(httpx.WithAuthContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/dispatch/jobs/job-1/driver-instruction-outcome" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		claims, ok := httpx.ClaimsFromRequest(r)
		if !ok || claims == nil || claims.TenantID != "tenant-1" {
			t.Fatalf("expected the call to act for tenant-1, got %+v", claims)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":{}}`))
	}), identity.SharedSecretVerifier("secret")))
	defer server.Close()

	reporter := NewDispatchDriverInstructionReporter(server.URL, identity.NewCaller("notification", nil, "secret"), server.Client())
	err := reporter.ReportDriverInstruction(context.Background(), application.DriverInstructionReport{
		TenantID: "tenant-1", JobID: "job-1", DriverID: "driver-1",
		Outcome: application.InstructionOutcomeNotSent, Channel: "whatsapp", Reason: "no WhatsApp number",
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if got.DriverID != "driver-1" || got.Outcome != "not_sent" || got.Reason != "no WhatsApp number" || got.Channel != "whatsapp" {
		t.Fatalf("unexpected body %+v", got)
	}
}

func TestDispatchDriverInstructionReporterSurfacesDispatchErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"job instruction can only be sent to the driver assigned to this job"}`))
	}))
	defer server.Close()

	reporter := NewDispatchDriverInstructionReporter(server.URL, identity.NewCaller("notification", nil, "secret"), server.Client())
	err := reporter.ReportDriverInstruction(context.Background(), application.DriverInstructionReport{TenantID: "tenant-1", JobID: "job-1", DriverID: "driver-9", Outcome: application.InstructionOutcomeSent})

	if err == nil || err.Error() != "dispatch service returned 409: job instruction can only be sent to the driver assigned to this job" {
		t.Fatalf("unexpected error %v", err)
	}
	if err := NewDispatchDriverInstructionReporter("", nil, nil).ReportDriverInstruction(context.Background(), application.DriverInstructionReport{}); err == nil {
		t.Fatal("expected an error when dispatch is not configured")
	}
}
