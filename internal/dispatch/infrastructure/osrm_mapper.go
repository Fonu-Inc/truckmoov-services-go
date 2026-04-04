package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type OsrmClient struct {
	baseURL    string
	httpClient *http.Client
}
type OsrmInput struct {
	Profile          string      `json:"profile,omitempty"`
	Coordinates      [][]float64 `json:"coordinates"`
	Alternatives     any         `json:"alternatives,omitempty"` // bool or int
	Steps            bool        `json:"steps,omitempty"`
	Annotations      any         `json:"annotations,omitempty"` // bool or []string
	Geometries       string      `json:"geometries,omitempty"`
	Overview         string      `json:"overview,omitempty"`
	ContinueStraight any         `json:"continue_straight,omitempty"` // bool or string
	Waypoints        []int       `json:"waypoints,omitempty"`
}
type OsrmResponse struct {
	Code      string         `json:"code"`
	Routes    []OsrmRoute    `json:"routes"`
	Waypoints []OsrmWaypoint `json:"waypoints,omitempty"`
}

type OsrmWaypoint struct {
	Name     string    `json:"name"`
	Location []float64 `json:"location"`
	Distance float64   `json:"distance,omitempty"`
}

type OsrmRoute struct {
	Distance float64  `json:"distance"`
	Duration float64  `json:"duration"`
	Geometry Geometry `json:"geometry"`
}

type Geometry struct {
	Coordinates [][]float64 `json:"coordinates"`
	Type        string      `json:"type"`
}

func NewOsrmClient() *OsrmClient {
	return NewOsrmClientWithConfig("http://osrm:5000", &http.Client{Timeout: 30 * time.Second})
}

func NewOsrmClientWithConfig(baseURL string, httpClient *http.Client) *OsrmClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &OsrmClient{
		baseURL:    baseURL,
		httpClient: httpClient,
	}
}

func (osrm *OsrmClient) CallRouteService(ctx context.Context, input OsrmInput) (OsrmResponse, error) {
	if len(input.Coordinates) < 2 {
		return OsrmResponse{}, fmt.Errorf("at least two coordinates are required")
	}

	profile := defaultOsrmProfile(input.Profile)
	endpoint := strings.TrimRight(osrm.baseURL, "/") + "/route/v1/" + profile + "/" + encodeCoordinates(input.Coordinates)

	values := url.Values{}
	if input.Alternatives != nil {
		values.Set("alternatives", fmt.Sprint(input.Alternatives))
	}
	if input.Steps {
		values.Set("steps", strconv.FormatBool(input.Steps))
	}
	if input.Annotations != nil {
		switch value := input.Annotations.(type) {
		case []string:
			values.Set("annotations", strings.Join(value, ","))
		default:
			values.Set("annotations", fmt.Sprint(value))
		}
	}
	if input.Geometries != "" {
		values.Set("geometries", input.Geometries)
	}
	if input.Overview != "" {
		values.Set("overview", input.Overview)
	}
	if input.ContinueStraight != nil {
		values.Set("continue_straight", fmt.Sprint(input.ContinueStraight))
	}
	if len(input.Waypoints) > 0 {
		waypoints := make([]string, 0, len(input.Waypoints))
		for _, waypoint := range input.Waypoints {
			waypoints = append(waypoints, strconv.Itoa(waypoint))
		}
		values.Set("waypoints", strings.Join(waypoints, ";"))
	}
	if encoded := values.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OsrmResponse{}, fmt.Errorf("build osrm request: %w", err)
	}

	resp, err := osrm.httpClient.Do(req)
	if err != nil {
		return OsrmResponse{}, fmt.Errorf("call osrm route service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return OsrmResponse{}, fmt.Errorf("osrm route failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out OsrmResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return OsrmResponse{}, fmt.Errorf("decode osrm response: %w", err)
	}
	if strings.TrimSpace(out.Code) != "" && !strings.EqualFold(out.Code, "ok") {
		return OsrmResponse{}, fmt.Errorf("osrm route failed: %s", out.Code)
	}
	out.Waypoints = waypointsFromCoordinates(input.Coordinates)

	return out, nil
}

func OsrmInputFromVroom(resp VroomResponse) (OsrmInput, error) {
	if len(resp.Routes) == 0 {
		return OsrmInput{}, fmt.Errorf("vroom response does not contain a route")
	}

	steps := resp.Routes[0].Steps
	coordinates := make([][]float64, 0, len(steps))
	for _, step := range steps {
		if len(step.Location) < 2 {
			continue
		}
		coordinates = append(coordinates, []float64{step.Location[0], step.Location[1]})
	}
	if len(coordinates) < 2 {
		return OsrmInput{}, fmt.Errorf("vroom response does not contain enough coordinates for osrm")
	}

	return OsrmInput{
		Profile:          "",
		Coordinates:      coordinates,
		Alternatives:     true,
		Steps:            true,
		Annotations:      false,
		Geometries:       "polyline",
		Overview:         "full",
		ContinueStraight: false,
	}, nil
}

func defaultOsrmProfile(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return "driving"
	}
	return profile
}

func encodeCoordinates(coordinates [][]float64) string {
	parts := make([]string, 0, len(coordinates))
	for _, coordinate := range coordinates {
		if len(coordinate) < 2 {
			continue
		}
		parts = append(parts, strconv.FormatFloat(coordinate[0], 'f', -1, 64)+","+strconv.FormatFloat(coordinate[1], 'f', -1, 64))
	}
	return strings.Join(parts, ";")
}

func waypointsFromCoordinates(coordinates [][]float64) []OsrmWaypoint {
	waypoints := make([]OsrmWaypoint, 0, len(coordinates))
	for _, coordinate := range coordinates {
		if len(coordinate) < 2 {
			continue
		}
		waypoints = append(waypoints, OsrmWaypoint{
			Location: []float64{coordinate[0], coordinate[1]},
		})
	}
	return waypoints
}
