package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type VroomRequest struct {
	Vehicles  []Vehicle  `json:"vehicles"`
	Shipments []Shipment `json:"shipments"`
}

type Vehicle struct {
	ID      int       `json:"id"`
	Start   []float64 `json:"start"`
	Profile string    `json:"profile"`
}

type Shipment struct {
	ID       int      `json:"id"`
	Pickup   StopTask `json:"pickup"`
	Delivery StopTask `json:"delivery"`
}

type StopTask struct {
	ID       int       `json:"id,omitempty"`
	Location []float64 `json:"location"`
	Service  int       `json:"service,omitempty"`
}

type VroomClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewVroomClient() *VroomClient {
	return &VroomClient{
		// NOTE: changing this string would mean you have changed the configs in the manifest file on k8s repo
		baseURL:    "http://vroom:6969",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type VroomSummary struct {
	Cost     int64 `json:"cost"`
	Duration int64 `json:"duration"`
	Distance int64 `json:"distance"`
}

type VroomStep struct {
	Type     string    `json:"type"`
	Job      int       `json:"job"`
	Arrival  int64     `json:"arrival"`
	Duration int64     `json:"duration"`
	Distance int64     `json:"distance"`
	Location []float64 `json:"location"`
}

type VroomRoute struct {
	Vehicle int         `json:"vehicle"`
	Steps   []VroomStep `json:"steps"`
}

type VroomUnassigned struct {
	ID int `json:"id"`
}

type VroomResponse struct {
	Code       int               `json:"code"`
	Summary    VroomSummary      `json:"summary"`
	Routes     []VroomRoute      `json:"routes"`
	Unassigned []VroomUnassigned `json:"unassigned"`
	Error      string            `json:"error"`
}

func (c *VroomClient) Optimize(ctx context.Context, req VroomRequest) (VroomResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return VroomResponse{}, fmt.Errorf("marshal vroom request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return VroomResponse{}, fmt.Errorf("build vroom request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return VroomResponse{}, fmt.Errorf("call vroom optimize: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return VroomResponse{}, fmt.Errorf("vroom optimize failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out VroomResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return VroomResponse{}, fmt.Errorf("decode vroom response: %w", err)
	}
	if out.Error != "" {
		return VroomResponse{}, fmt.Errorf("vroom optimize failed: %s", out.Error)
	}
	return out, nil
}
