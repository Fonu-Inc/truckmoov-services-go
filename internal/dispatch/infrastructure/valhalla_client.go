package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ValhallaClient struct {
	baseUrl    string
	httpClient *http.Client
}

func NewValhallaClient(url string) *ValhallaClient {
	return NewValhallaClientWithConfig(url, &http.Client{Timeout: 30 * time.Second})
}

func NewValhallaClientWithConfig(baseUrl string, client *http.Client) *ValhallaClient {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &ValhallaClient{
		baseUrl:    baseUrl,
		httpClient: client,
	}
}

type ValhallaMatrixResponse struct {
	ID               string                 `json:"id"`
	Algorithm        string                 `json:"algorithm"`
	Units            string                 `json:"units"`
	Warnings         []string               `json:"warnings,omitempty"`
	SourcesToTargets [][]ValhallaMatrixCell `json:"sources_to_targets"`
	ErrorCode        int                    `json:"error_code,omitempty"`
	Error            string                 `json:"error,omitempty"`
}

type ValhallaMatrixCell struct {
	Time float64 `json:"time"`
}

type ValhallaLocStruct struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
type ValhallaMatrixApiInput struct {
	Sources []ValhallaLocStruct `json:"sources"`
	Targets []ValhallaLocStruct `json:"targets"`
	Costing string              `json:"costing"`
}

func (v *ValhallaClient) GetMatrix(ctx context.Context, payload []SolverNodeRef) ([]int64, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("valhalla matrix requires at least one node")
	}

	locations := make([]ValhallaLocStruct, 0, len(payload))
	for _, node := range payload {
		locations = append(locations, ValhallaLocStruct{
			Lat: node.location.Latitude,
			Lon: node.location.Longitude,
		})
	}

	input := &ValhallaMatrixApiInput{
		Sources: locations,
		Targets: locations,
		Costing: "truck",
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal valhalla matrix request: %w", err)
	}
	endpoint := v.baseUrl + "/sources_to_targets?json=" + url.QueryEscape(string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create valhalla matrix request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call valhalla matrix service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("valhalla matrix returned status %s", resp.Status)
	}

	var output ValhallaMatrixResponse
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return nil, fmt.Errorf("decode valhalla matrix response: %w", err)
	}
	if output.Error != "" {
		return nil, fmt.Errorf("valhalla matrix error: %s", output.Error)
	}
	if len(output.SourcesToTargets) != len(payload) {
		return nil, fmt.Errorf("valhalla matrix size mismatch: got %d rows want %d", len(output.SourcesToTargets), len(payload))
	}

	matrix := make([]int64, 0, len(payload)*len(payload))
	for rowIdx, row := range output.SourcesToTargets {
		if len(row) != len(payload) {
			return nil, fmt.Errorf("valhalla matrix row %d size mismatch: got %d cols want %d", rowIdx, len(row), len(payload))
		}
		for _, cell := range row {
			matrix = append(matrix, int64(math.Round(cell.Time)))
		}
	}

	return matrix, nil
}

type OptimizeRouteInput struct {
	Locations      []ValhallaLocStruct `json:"locations"`
	Costing        string              `json:"costing"`
	Units          string              `json:"units"`
	DirectionsType string              `json:"directions_type,omitempty"`
	Alternates     int                 `json:"alternates,omitempty"`
	ID             string              `json:"id,omitempty"`
}

type ValhallaRouteSummary struct {
	Length float64 `json:"length"`
	Time   float64 `json:"time"`
}

type ValhallaRouteLeg struct {
	Summary   ValhallaRouteSummary    `json:"summary"`
	Shape     string                  `json:"shape,omitempty"`
	Maneuvers []ValhallaRouteManeuver `json:"maneuvers,omitempty"`
}

type ValhallaRouteManeuver struct {
	Type                             int      `json:"type"`
	Instruction                      string   `json:"instruction,omitempty"`
	VerbalTransitionAlertInstruction string   `json:"verbal_transition_alert_instruction,omitempty"`
	VerbalPreTransitionInstruction   string   `json:"verbal_pre_transition_instruction,omitempty"`
	VerbalPostTransitionInstruction  string   `json:"verbal_post_transition_instruction,omitempty"`
	StreetNames                      []string `json:"street_names,omitempty"`
	Time                             float64  `json:"time"`
	Length                           float64  `json:"length"`
	BeginShapeIndex                  int      `json:"begin_shape_index,omitempty"`
	EndShapeIndex                    int      `json:"end_shape_index,omitempty"`
	TravelMode                       string   `json:"travel_mode,omitempty"`
	TravelType                       string   `json:"travel_type,omitempty"`
	BearingBefore                    int      `json:"bearing_before,omitempty"`
	BearingAfter                     int      `json:"bearing_after,omitempty"`
}

type ValhallaRouteTrip struct {
	Legs    []ValhallaRouteLeg   `json:"legs"`
	Summary ValhallaRouteSummary `json:"summary"`
}

type ValhallaAlternateRoute struct {
	Trip    *ValhallaRouteTrip   `json:"trip,omitempty"`
	Legs    []ValhallaRouteLeg   `json:"legs,omitempty"`
	Summary ValhallaRouteSummary `json:"summary,omitempty"`
}

func (a ValhallaAlternateRoute) routeTrip() ValhallaRouteTrip {
	if a.Trip != nil {
		return *a.Trip
	}
	return ValhallaRouteTrip{
		Legs:    a.Legs,
		Summary: a.Summary,
	}
}

type ValhallaRouteResponse struct {
	Trip       ValhallaRouteTrip        `json:"trip"`
	Alternates []ValhallaAlternateRoute `json:"alternates,omitempty"`
	ErrorCode  int                      `json:"error_code,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

func (v *ValhallaClient) OptimizeRoute(ctx context.Context, payload []SolverNodeRef) (*ValhallaRouteResponse, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("valhalla route requires at least two nodes")
	}

	locations := make([]ValhallaLocStruct, 0, len(payload))
	for _, node := range payload {
		locations = append(locations, ValhallaLocStruct{
			Lat: node.location.Latitude,
			Lon: node.location.Longitude,
		})
	}

	input := OptimizeRouteInput{
		Locations: locations,
		Costing:   "truck",
		Units:     "kilometers",
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal valhalla route request: %w", err)
	}
	endpoint := v.baseUrl + "/optimized_route?json=" + url.QueryEscape(string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create valhalla route request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call valhalla optimized route service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("valhalla optimized route returned status %s", resp.Status)
	}

	var output ValhallaRouteResponse
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return nil, fmt.Errorf("decode valhalla route response: %w", err)
	}
	if output.Error != "" {
		return nil, fmt.Errorf("valhalla optimized route error: %s", output.Error)
	}

	return &output, nil
}

func (v *ValhallaClient) GetTurnByTurnDirections(ctx context.Context, payload []SolverNodeRef) (*ValhallaRouteResponse, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("valhalla route requires at least two nodes")
	}

	locations := make([]ValhallaLocStruct, 0, len(payload))
	for _, node := range payload {
		locations = append(locations, ValhallaLocStruct{
			Lat: node.location.Latitude,
			Lon: node.location.Longitude,
		})
	}

	input := OptimizeRouteInput{
		Locations:      locations,
		Costing:        "truck",
		Units:          "kilometers",
		DirectionsType: "instructions",
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal valhalla route request: %w", err)
	}
	endpoint := v.baseUrl + "/route?json=" + url.QueryEscape(string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create valhalla route request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call valhalla route service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("valhalla route returned status %s", resp.Status)
	}

	var output ValhallaRouteResponse
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return nil, fmt.Errorf("decode valhalla route response: %w", err)
	}
	if output.Error != "" {
		return nil, fmt.Errorf("valhalla route error: %s", output.Error)
	}

	return &output, nil
}

func (v *ValhallaClient) GetLegAlternates(ctx context.Context, from, to SolverNodeRef, alternates int) ([]string, error) {
	if alternates <= 0 {
		return nil, nil
	}

	input := OptimizeRouteInput{
		Locations: []ValhallaLocStruct{
			{Lat: from.location.Latitude, Lon: from.location.Longitude},
			{Lat: to.location.Latitude, Lon: to.location.Longitude},
		},
		Costing:        "truck",
		Units:          "kilometers",
		DirectionsType: "instructions",
		Alternates:     alternates,
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal valhalla alternate route request: %w", err)
	}
	endpoint := v.baseUrl + "/route?json=" + url.QueryEscape(string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create valhalla alternate route request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call valhalla alternate route service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("valhalla alternate route returned status %s", resp.Status)
	}

	var output ValhallaRouteResponse
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return nil, fmt.Errorf("decode valhalla alternate route response: %w", err)
	}
	if output.Error != "" {
		return nil, fmt.Errorf("valhalla alternate route error: %s", output.Error)
	}

	shapes := make([]string, 0, len(output.Alternates))
	for _, alternate := range output.Alternates {
		trip := alternate.routeTrip()
		if len(trip.Legs) == 0 {
			continue
		}
		shape := strings.TrimSpace(trip.Legs[0].Shape)
		if shape == "" {
			continue
		}
		shapes = append(shapes, shape)
	}

	if len(shapes) == 0 {
		return nil, nil
	}
	return shapes, nil
}
