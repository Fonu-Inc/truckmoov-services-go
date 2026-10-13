// Package valhalla is a small client for the Valhalla routing engine's
// map-matching API, shared by services that only need to ask which road a
// trace was driven on.
package valhalla

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

type TracePoint struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Time int64   `json:"time,omitempty"`
}

type Edge struct {
	Names     []string `json:"names"`
	RoadClass string   `json:"road_class"`
	// SpeedLimit is the posted limit in km/h; 0 when OSM has none.
	SpeedLimit SpeedLimit `json:"speed_limit"`
	WayID      int64      `json:"way_id"`
}

// SpeedLimit decodes a numeric limit and reads any other value, such as
// "unlimited", as no posted limit rather than failing the whole match.
type SpeedLimit int

func (l *SpeedLimit) UnmarshalJSON(raw []byte) error {
	var kph float64
	if json.Unmarshal(raw, &kph) == nil {
		*l = SpeedLimit(kph)
	}
	return nil
}

type MatchedPoint struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Type string  `json:"type"`
	// EdgeIndex is absent for an unmatched point.
	EdgeIndex              *int    `json:"edge_index"`
	DistanceFromTracePoint float64 `json:"distance_from_trace_point"`
}

type TraceResult struct {
	Edges         []Edge         `json:"edges"`
	MatchedPoints []MatchedPoint `json:"matched_points"`
}

var traceFilters = map[string]any{
	"action": "include",
	"attributes": []string{
		"edge.names", "edge.road_class", "edge.speed_limit", "edge.way_id",
		"matched.point", "matched.type", "matched.edge_index", "matched.distance_from_trace_point",
	},
}

// TraceAttributes map-matches a trace for a truck and returns the matched
// point for every trace point and the edges they sit on.
func (c *Client) TraceAttributes(ctx context.Context, trace []TracePoint) (TraceResult, error) {
	body, err := json.Marshal(map[string]any{
		"shape":       trace,
		"costing":     "truck",
		"shape_match": "map_snap",
		"filters":     traceFilters,
	})
	if err != nil {
		return TraceResult{}, fmt.Errorf("marshal trace_attributes request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/trace_attributes", bytes.NewReader(body))
	if err != nil {
		return TraceResult{}, fmt.Errorf("create trace_attributes request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TraceResult{}, fmt.Errorf("call valhalla trace_attributes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TraceResult{}, fmt.Errorf("valhalla trace_attributes returned status %s", resp.Status)
	}

	var result TraceResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TraceResult{}, fmt.Errorf("decode trace_attributes response: %w", err)
	}
	return result, nil
}
