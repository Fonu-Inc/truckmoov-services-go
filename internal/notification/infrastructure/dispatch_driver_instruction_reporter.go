package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

var _ application.DriverInstructionReporter = (*DispatchDriverInstructionReporter)(nil)

// DispatchDriverInstructionReporter tells dispatch whether a driver was
// really messaged, so the job timeline records what happened.
type DispatchDriverInstructionReporter struct {
	baseURL string
	caller  *identity.Caller
	client  *http.Client
}

type driverInstructionOutcomeBody struct {
	DriverID string `json:"driver_id"`
	Outcome  string `json:"outcome"`
	Channel  string `json:"channel,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func NewDispatchDriverInstructionReporter(baseURL string, caller *identity.Caller, client *http.Client) *DispatchDriverInstructionReporter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &DispatchDriverInstructionReporter{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), caller: caller, client: client}
}

func (r *DispatchDriverInstructionReporter) ReportDriverInstruction(ctx context.Context, report application.DriverInstructionReport) error {
	if r.baseURL == "" {
		return fmt.Errorf("dispatch service url is not configured")
	}
	body, err := json.Marshal(driverInstructionOutcomeBody{
		DriverID: strings.TrimSpace(report.DriverID),
		Outcome:  report.Outcome,
		Channel:  report.Channel,
		Reason:   report.Reason,
	})
	if err != nil {
		return fmt.Errorf("marshal driver instruction outcome: %w", err)
	}

	endpoint := r.baseURL + "/dispatch/jobs/" + url.PathEscape(strings.TrimSpace(report.JobID)) + "/driver-instruction-outcome"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build dispatch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	r.caller.Authorize(req, "dispatch", identity.ActingFor{TenantID: report.TenantID, LegacyRole: "DISPATCHER"})

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("send dispatch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	message := extractDispatchErrorMessage(responseBody)
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("dispatch service returned %d: %s", resp.StatusCode, message)
}
