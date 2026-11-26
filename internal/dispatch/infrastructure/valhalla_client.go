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

	"github.com/truckmoov/services-go/internal/dispatch/application"
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

// Valhalla returns null time and distance for a pair it cannot route between.
type ValhallaMatrixCell struct {
	Time     *float64 `json:"time"`
	Distance *float64 `json:"distance"`
}

// unreachableSeconds is far beyond any route horizon, so the solver's
// duration dimension rejects the arc instead of treating it as free.
const unreachableSeconds int64 = math.MaxInt32

// truckTopSpeedKPH caps planned truck speed at the highway limit; Valhalla's
// default of 120 km/h makes ETAs optimistic.
const truckTopSpeedKPH = 80

type valhallaTruckOptions struct {
	TopSpeed int `json:"top_speed"`
}

type valhallaCostingOptions struct {
	Truck valhallaTruckOptions `json:"truck"`
}

func truckCostingOptions() *valhallaCostingOptions {
	return &valhallaCostingOptions{Truck: valhallaTruckOptions{TopSpeed: truckTopSpeedKPH}}
}

func (c ValhallaMatrixCell) seconds() int64 {
	if c.Time == nil {
		return unreachableSeconds
	}
	return int64(math.Round(*c.Time))
}

type ValhallaLocStruct struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
type ValhallaMatrixApiInput struct {
	Sources        []ValhallaLocStruct     `json:"sources"`
	Targets        []ValhallaLocStruct     `json:"targets"`
	Costing        string                  `json:"costing"`
	CostingOptions *valhallaCostingOptions `json:"costing_options,omitempty"`
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
		Sources:        locations,
		Targets:        locations,
		Costing:        "truck",
		CostingOptions: truckCostingOptions(),
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
			matrix = append(matrix, cell.seconds())
		}
	}

	return matrix, nil
}

type OptimizeRouteInput struct {
	Locations      []ValhallaLocStruct     `json:"locations"`
	Costing        string                  `json:"costing"`
	CostingOptions *valhallaCostingOptions `json:"costing_options,omitempty"`
	Units          string                  `json:"units"`
	DirectionsType string                  `json:"directions_type,omitempty"`
	Alternates     int                     `json:"alternates,omitempty"`
	ID             string                  `json:"id,omitempty"`
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
		CostingOptions: truckCostingOptions(),
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

// RoutePreview is one truck route through the points in the order supplied: one encoded
// polyline6 per leg (joined strings would not decode as one polyline), the duration in
// seconds and the distance in kilometres. It neither reorders stops nor consults the solver.
func (v *ValhallaClient) RoutePreview(ctx context.Context, points []ValhallaLocStruct) ([]string, int64, float64, error) {
	if len(points) < 2 {
		return nil, 0, 0, fmt.Errorf("valhalla route requires at least two points")
	}
	var output ValhallaRouteResponse
	err := v.get(ctx, "route", OptimizeRouteInput{
		Locations:      points,
		Costing:        "truck",
		CostingOptions: truckCostingOptions(),
		Units:          "kilometers",
		DirectionsType: "none",
	}, &output)
	if err != nil {
		return nil, 0, 0, err
	}
	shapes := make([]string, 0, len(output.Trip.Legs))
	for _, leg := range output.Trip.Legs {
		if leg.Shape != "" {
			shapes = append(shapes, leg.Shape)
		}
	}
	if len(shapes) == 0 {
		return nil, 0, 0, fmt.Errorf("valhalla route response carried no geometry")
	}
	return shapes, int64(math.Round(output.Trip.Summary.Time)), output.Trip.Summary.Length, nil
}

// RouteShape returns the legs' polylines joined with ";" for the overview map, which
// draws the route a driver was already given, so ordering is an input, not a question.
func (v *ValhallaClient) RouteShape(ctx context.Context, points []ValhallaLocStruct) (string, string, error) {
	shapes, _, _, err := v.RoutePreview(ctx, points)
	if err != nil {
		return "", "", err
	}
	return strings.Join(shapes, ";"), "polyline6", nil
}

// RouteEstimate returns the routed duration in seconds and distance in
// kilometres for the given ordered points.
func (v *ValhallaClient) RouteEstimate(ctx context.Context, points []ValhallaLocStruct) (int64, float64, error) {
	_, seconds, km, err := v.RoutePreview(ctx, points)
	return seconds, km, err
}

// DistanceMatrix returns kilometres and seconds between every pair of points, nil
// where no road joins a pair.
func (v *ValhallaClient) DistanceMatrix(ctx context.Context, points []ValhallaLocStruct) ([][]*float64, [][]*int64, error) {
	var output ValhallaMatrixResponse
	err := v.get(ctx, "sources_to_targets", ValhallaMatrixApiInput{
		Sources:        points,
		Targets:        points,
		Costing:        "truck",
		CostingOptions: truckCostingOptions(),
	}, &output)
	if err != nil {
		return nil, nil, err
	}
	if len(output.SourcesToTargets) != len(points) {
		return nil, nil, fmt.Errorf("valhalla matrix size mismatch: got %d rows want %d", len(output.SourcesToTargets), len(points))
	}
	km := make([][]*float64, len(points))
	seconds := make([][]*int64, len(points))
	for i, row := range output.SourcesToTargets {
		if len(row) != len(points) {
			return nil, nil, fmt.Errorf("valhalla matrix row %d size mismatch: got %d cols want %d", i, len(row), len(points))
		}
		km[i] = make([]*float64, len(row))
		seconds[i] = make([]*int64, len(row))
		for j, cell := range row {
			if cell.Time == nil || cell.Distance == nil {
				continue
			}
			s := int64(math.Round(*cell.Time))
			d := *cell.Distance
			km[i][j], seconds[i][j] = &d, &s
		}
	}
	return km, seconds, nil
}

// valhallaClientError reports Valhalla's answers about the request itself (no road near a
// point, no path, a distance limit) rather than a fault in the router.
func valhallaClientError(code int) bool { return code >= 100 && code < 500 }

// get calls a Valhalla service with input as its json query and decodes the reply into out.
func (v *ValhallaClient) get(ctx context.Context, service string, input, out any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal valhalla %s request: %w", service, err)
	}
	endpoint := v.baseUrl + "/" + service + "?json=" + url.QueryEscape(string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create valhalla %s request: %w", service, err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call valhalla %s: %w", service, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			ErrorCode int    `json:"error_code"`
			Error     string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&failure)
		if resp.StatusCode == http.StatusBadRequest && valhallaClientError(failure.ErrorCode) {
			return fmt.Errorf("%w: %s (valhalla %d)", application.ErrNoRoadRoute, failure.Error, failure.ErrorCode)
		}
		return fmt.Errorf("valhalla %s returned status %s: %s", service, resp.Status, failure.Error)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode valhalla %s response: %w", service, err)
	}
	return nil
}

func (v *ValhallaClient) GetLegAlternates(ctx context.Context, from, to SolverNodeRef, alternates int) ([]ValhallaRouteLeg, error) {
	if alternates <= 0 {
		return nil, nil
	}

	input := OptimizeRouteInput{
		Locations: []ValhallaLocStruct{
			{Lat: from.location.Latitude, Lon: from.location.Longitude},
			{Lat: to.location.Latitude, Lon: to.location.Longitude},
		},
		Costing:        "truck",
		CostingOptions: truckCostingOptions(),
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

	legs := make([]ValhallaRouteLeg, 0, len(output.Alternates))
	for _, alternate := range output.Alternates {
		trip := alternate.routeTrip()
		if len(trip.Legs) == 0 {
			continue
		}
		leg := trip.Legs[0]
		if strings.TrimSpace(leg.Shape) == "" {
			continue
		}
		legs = append(legs, leg)
	}

	if len(legs) == 0 {
		return nil, nil
	}
	return legs, nil
}
