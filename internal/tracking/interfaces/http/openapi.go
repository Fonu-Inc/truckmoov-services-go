package trackinghttp

import (
	"net/http"

	httpx "github.com/truckmoov/services-go/pkg/http"
)

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Tracking Service API Docs</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    const basePath = window.location.pathname.replace(/\/+$/, "");
    const specUrl = basePath + "/openapi.json";
    window.ui = SwaggerUIBundle({
      url: specUrl,
      dom_id: "#swagger-ui",
      deepLinking: true,
      displayRequestDuration: true,
      defaultModelsExpandDepth: 1
    });
  </script>
</body>
</html>`

func (h *Handler) docsUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	httpx.WriteHTML(w, swaggerUIHTML)
}

func (h *Handler) docsOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	httpx.WriteDocument(w, trackingOpenAPISpec())
}

func trackingOpenAPISpec() map[string]any {
	vehicleIDParameter := map[string]any{
		"name":     "vehicleId",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string"},
	}
	deviceIDParameter := map[string]any{
		"name":     "deviceId",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string"},
	}
	vendorParameter := map[string]any{
		"name":        "vendor",
		"in":          "path",
		"required":    true,
		"description": "Hardware vendor whose payload format is being posted.",
		"schema":      map[string]any{"type": "string", "enum": []string{"generic", "teltonika", "traccar", "queclink"}},
	}
	limitQueryParameter := map[string]any{
		"name":        "limit",
		"in":          "query",
		"required":    false,
		"description": "Maximum number of records to return. Omit or 0 for the server default.",
		"schema":      map[string]any{"type": "integer", "minimum": 0},
	}
	apiKeySecurity := []map[string]any{{"ApiKeyAuth": []string{}}}
	bearerSecurity := []map[string]any{{"BearerAuth": []string{}}}

	return httpx.ApplyContract(map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Tracking Service API",
			"version":     "0.1.0",
			"description": "Live vehicle tracking: GPS/telemetry ingest (generic + vendor adapters), latest-location and history reads, device-detail tabs (overview/connectivity/telemetry/firmware), and alert publishing.",
		},
		"servers": []map[string]any{
			{"url": "/"},
		},
		"tags": []map[string]any{
			{"name": "tracking", "description": "Tracking service endpoints"},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"ApiKeyAuth": map[string]any{
					"type":        "apiKey",
					"in":          "header",
					"name":        "X-Api-Key",
					"description": "Device/gateway API key for ingest endpoints.",
				},
				"BearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
					"description":  "Access token. The tenant context is derived from the token; tenant_id/tenantId query params are rejected.",
				},
			},
			"schemas": map[string]any{
				"IngestRequest": map[string]any{
					"type":        "object",
					"description": "Standard normalised location payload accepted by POST /tracking/ingest and the generic vendor adapter.",
					"properties": map[string]any{
						"imei":       map[string]any{"type": "string", "description": "Device IMEI. Tenant, vehicle and device are resolved from it."},
						"tenant_id":  map[string]any{"type": "string", "deprecated": true, "description": "Ignored."},
						"vehicle_id": map[string]any{"type": "string", "deprecated": true, "description": "Ignored."},
						"device_id":  map[string]any{"type": "string", "deprecated": true, "description": "Ignored."},
						"lat":        map[string]any{"type": "number"},
						"lng":        map[string]any{"type": "number"},
						"speed_kph":  map[string]any{"type": "number", "nullable": true},
						"heading":    map[string]any{"type": "number", "nullable": true},
						"accuracy_m": map[string]any{"type": "number", "nullable": true},
						"fix_time":   map[string]any{"type": "string", "format": "date-time", "description": "RFC3339 timestamp of the GPS fix."},
					},
					"required": []string{"imei", "lat", "lng"},
				},
				"TeltonikaPayload": map[string]any{
					"type":        "object",
					"description": "Teltonika FMB-series AVL payload. unique key is the IMEI; tenant/vehicle/device are resolved from the IMEI identity cache.",
					"properties": map[string]any{
						"imei":      map[string]any{"type": "string"},
						"timestamp": map[string]any{"type": "integer", "format": "int64", "description": "Unix seconds; converted to RFC3339 fix_time."},
						"lat":       map[string]any{"type": "number"},
						"lng":       map[string]any{"type": "number"},
						"speed":     map[string]any{"type": "number", "description": "km/h"},
						"angle":     map[string]any{"type": "number", "description": "Heading in degrees."},
						"altitude":  map[string]any{"type": "number"},
						"tenant_id": map[string]any{"type": "string"},
					},
				},
				"QueclinkPayload": map[string]any{
					"type":        "object",
					"description": "Queclink GV-series payload. unique_id is the IMEI.",
					"properties": map[string]any{
						"unique_id":  map[string]any{"type": "string"},
						"latitude":   map[string]any{"type": "number"},
						"longitude":  map[string]any{"type": "number"},
						"speed_kmh":  map[string]any{"type": "number"},
						"course":     map[string]any{"type": "number", "description": "Heading in degrees."},
						"accuracy_m": map[string]any{"type": "number"},
						"fix_time":   map[string]any{"type": "string", "format": "date-time"},
						"tenant_id":  map[string]any{"type": "string"},
					},
				},
				"TraccarPayload": map[string]any{
					"type":        "object",
					"description": "Traccar position-forwarding JSON (forward.json=true). device.uniqueId is the IMEI; speed arrives in knots and is converted to km/h. Tenant is resolved from the IMEI identity cache.",
					"properties": map[string]any{
						"position": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"latitude":   map[string]any{"type": "number"},
								"longitude":  map[string]any{"type": "number"},
								"speed":      map[string]any{"type": "number", "description": "Knots."},
								"course":     map[string]any{"type": "number"},
								"deviceTime": map[string]any{"type": "string", "format": "date-time"},
								"fixTime":    map[string]any{"type": "string", "format": "date-time"},
								"attributes": map[string]any{
									"type":                 "object",
									"description":          "Optional non-GPS telemetry. Recognised keys: batteryLevel/battery, rssi/signal, fuel/fuelLevel, ignition, firmware/version.",
									"additionalProperties": true,
								},
							},
						},
						"device": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"uniqueId": map[string]any{"type": "string", "description": "Treated as the device IMEI."},
							},
						},
					},
				},
				"IngestLocationResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"published":   map[string]any{"type": "boolean"},
						"subject":     map[string]any{"type": "string"},
						"event_id":    map[string]any{"type": "string"},
						"skipped":     map[string]any{"type": "boolean", "description": "True when the fix was filtered (e.g. stale or low-accuracy) and not persisted."},
						"skip_reason": map[string]any{"type": "string"},
					},
				},
				"AlertRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tenant_id":  map[string]any{"type": "string"},
						"alert_type": map[string]any{"type": "string"},
						"severity":   map[string]any{"type": "string"},
						"vehicle_id": map[string]any{"type": "string"},
						"job_id":     map[string]any{"type": "string"},
						"details":    map[string]any{"description": "Arbitrary alert metadata.", "nullable": true},
					},
					"required": []string{"alert_type", "severity"},
				},
				"PublishAlertResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"published": map[string]any{"type": "boolean"},
						"subject":   map[string]any{"type": "string"},
						"event_id":  map[string]any{"type": "string"},
						"alert_id":  map[string]any{"type": "string"},
					},
				},
				"LatestLocation": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tenant_id":   map[string]any{"type": "string"},
						"vehicle_id":  map[string]any{"type": "string"},
						"device_id":   map[string]any{"type": "string"},
						"latitude":    map[string]any{"type": "number"},
						"longitude":   map[string]any{"type": "number"},
						"speed_kph":   map[string]any{"type": "number", "nullable": true},
						"heading":     map[string]any{"type": "number", "nullable": true},
						"accuracy_m":  map[string]any{"type": "number", "nullable": true},
						"fix_time":    map[string]any{"type": "string", "format": "date-time"},
						"observed_at": map[string]any{"type": "string", "format": "date-time"},
						"event_id":    map[string]any{"type": "string"},
					},
				},
				"VehicleListResponse": map[string]any{
					"type":  "array",
					"items": map[string]any{"$ref": "#/components/schemas/LatestLocation"},
				},
				"VehicleHistoryResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"vehicle_id": map[string]any{"type": "string"},
						"history":    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/LatestLocation"}},
					},
				},
				"DeviceOverview": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"device_id":        map[string]any{"type": "string"},
						"vehicle_id":       map[string]any{"type": "string"},
						"imei":             map[string]any{"type": "string"},
						"health":           map[string]any{"type": "string", "enum": []string{"online", "degraded", "offline", "unknown"}},
						"last_seen":        map[string]any{"type": "string", "format": "date-time"},
						"signal_strength":  map[string]any{"type": "number", "nullable": true},
						"battery_level":    map[string]any{"type": "number", "nullable": true},
						"latitude":         map[string]any{"type": "number", "nullable": true},
						"longitude":        map[string]any{"type": "number", "nullable": true},
						"firmware_version": map[string]any{"type": "string"},
						"sim": map[string]any{
							"type":        "object",
							"nullable":    true,
							"description": "SIM card assigned to the device, cached from the auth service's sim.changed events. Null when no SIM is linked.",
							"properties": map[string]any{
								"device_id":     map[string]any{"type": "string"},
								"iccid":         map[string]any{"type": "string"},
								"msisdn":        map[string]any{"type": "string"},
								"carrier":       map[string]any{"type": "string"},
								"plan":          map[string]any{"type": "string"},
								"status":        map[string]any{"type": "string"},
								"data_usage_mb": map[string]any{"type": "number"},
								"data_limit_mb": map[string]any{"type": "number"},
								"expiry_date":   map[string]any{"type": "string", "format": "date-time"},
							},
						},
					},
				},
				"DeviceConnectivity": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"device_id":                  map[string]any{"type": "string"},
						"health":                     map[string]any{"type": "string", "enum": []string{"online", "degraded", "offline", "unknown"}},
						"last_contact":               map[string]any{"type": "string", "format": "date-time"},
						"last_telemetry":             map[string]any{"type": "string", "format": "date-time"},
						"seconds_since_contact":      map[string]any{"type": "number", "nullable": true},
						"status_timeout_seconds":     map[string]any{"type": "integer"},
						"reporting_interval_seconds": map[string]any{"type": "number", "nullable": true},
						"sample_count_24h":           map[string]any{"type": "integer"},
						"heartbeat": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"expected_interval_seconds": map[string]any{"type": "number", "nullable": true},
								"online_threshold_seconds":  map[string]any{"type": "integer"},
								"offline_threshold_seconds": map[string]any{"type": "integer"},
							},
						},
						"last_health_check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"at":     map[string]any{"type": "string", "format": "date-time"},
								"result": map[string]any{"type": "string", "enum": []string{"online", "degraded", "offline", "unknown"}},
							},
						},
					},
				},
				"TelemetrySample": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tenant_id":        map[string]any{"type": "string"},
						"device_id":        map[string]any{"type": "string"},
						"vehicle_id":       map[string]any{"type": "string"},
						"imei":             map[string]any{"type": "string"},
						"latitude":         map[string]any{"type": "number"},
						"longitude":        map[string]any{"type": "number"},
						"speed_kph":        map[string]any{"type": "number", "nullable": true},
						"heading":          map[string]any{"type": "number", "nullable": true},
						"accuracy_m":       map[string]any{"type": "number", "nullable": true},
						"battery_level":    map[string]any{"type": "number", "nullable": true},
						"signal_strength":  map[string]any{"type": "number", "nullable": true},
						"fuel_level":       map[string]any{"type": "number", "nullable": true},
						"engine_on":        map[string]any{"type": "boolean", "nullable": true},
						"firmware_version": map[string]any{"type": "string"},
						"protocol":         map[string]any{"type": "string"},
						"fix_time":         map[string]any{"type": "string", "format": "date-time", "nullable": true},
						"observed_at":      map[string]any{"type": "string", "format": "date-time"},
						"event_id":         map[string]any{"type": "string"},
					},
				},
				"TelemetryAggregates": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"sample_count":         map[string]any{"type": "integer"},
						"avg_interval_seconds": map[string]any{"type": "number", "nullable": true},
						"data_quality_pct":     map[string]any{"type": "number", "nullable": true},
						"window_start":         map[string]any{"type": "string", "format": "date-time"},
						"window_end":           map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"DeviceTelemetry": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"device_id":      map[string]any{"type": "string"},
						"latest":         map[string]any{"allOf": []map[string]any{{"$ref": "#/components/schemas/TelemetrySample"}}, "nullable": true},
						"history":        map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/TelemetrySample"}},
						"aggregates_24h": map[string]any{"$ref": "#/components/schemas/TelemetryAggregates"},
					},
				},
				"FirmwareReport": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"version":    map[string]any{"type": "string"},
						"first_seen": map[string]any{"type": "string", "format": "date-time"},
						"last_seen":  map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"DeviceFirmware": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"device_id":                  map[string]any{"type": "string"},
						"current_version":            map[string]any{"type": "string"},
						"protocol":                   map[string]any{"type": "string", "description": "Device protocol from the Traccar payload (e.g. teltonika, gt06); for direct non-Traccar adapters this is the ingest source (generic/teltonika/queclink)."},
						"history":                    map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/FirmwareReport"}},
						"reporting_interval_seconds": map[string]any{"type": "number", "nullable": true},
					},
				},
			},
		},
		"paths": map[string]any{
			"/tracking/health": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Health check",
					"operationId": "trackingHealth",
					"responses": map[string]any{
						"200": map[string]any{"description": "Service is healthy"},
					},
				},
			},
			"/tracking/ready": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Readiness probe",
					"operationId": "trackingReady",
					"responses": map[string]any{
						"200": map[string]any{"description": "Service is ready"},
						"503": map[string]any{"description": "Service is not ready"},
					},
				},
			},
			"/tracking/ingest": map[string]any{
				"post": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Ingest a normalised GPS/telemetry fix",
					"operationId": "ingestLocation",
					"security":    apiKeySecurity,
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/IngestRequest"},
							},
						},
					},
					"responses": map[string]any{
						"202": map[string]any{
							"description": "Fix accepted (or filtered)",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/IngestLocationResult"}},
							},
						},
						"400": map[string]any{
							"description": "Invalid body",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
						"401": map[string]any{
							"description": "Invalid or missing API key",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
						"503": map[string]any{
							"description": "Ingest is not configured, or a dependency is unavailable; retry",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
					},
				},
			},
			"/tracking/ingest/vendor/{vendor}": map[string]any{
				"post": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Ingest a vendor-specific fix",
					"description": "Normalises a vendor payload (teltonika/traccar/queclink/generic) to the canonical fix before persistence. The request body shape depends on {vendor}.",
					"operationId": "ingestVendorLocation",
					"security":    apiKeySecurity,
					"parameters":  []any{vendorParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"oneOf": []map[string]any{
										{"$ref": "#/components/schemas/IngestRequest"},
										{"$ref": "#/components/schemas/TeltonikaPayload"},
										{"$ref": "#/components/schemas/TraccarPayload"},
										{"$ref": "#/components/schemas/QueclinkPayload"},
									},
								},
							},
						},
					},
					"responses": map[string]any{
						"202": map[string]any{
							"description": "Fix accepted (or filtered)",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/IngestLocationResult"}},
							},
						},
						"400": map[string]any{
							"description": "Unknown vendor or invalid payload",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
						"401": map[string]any{
							"description": "Invalid or missing API key",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
						"503": map[string]any{
							"description": "Ingest is not configured, or a dependency is unavailable; retry",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
					},
				},
			},
			"/tracking/alerts": map[string]any{
				"post": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Publish a tracking alert",
					"operationId": "publishTrackingAlert",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/AlertRequest"},
							},
						},
					},
					"responses": map[string]any{
						"202": map[string]any{
							"description": "Alert published",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/PublishAlertResult"}},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
					},
				},
			},
			"/tracking/vehicles": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "List latest known vehicle locations",
					"description": "Returns the latest fix per vehicle for the authenticated tenant.",
					"operationId": "listVehicleLocations",
					"security":    bearerSecurity,
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Latest locations",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/VehicleListResponse"}},
							},
						},
						"400": map[string]any{
							"description": "tenant_id/tenantId supplied as a query param",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"}},
							},
						},
						"401": map[string]any{"description": "Authenticated tenant context required"},
						"503": map[string]any{"description": "Tracking query not configured"},
					},
				},
			},
			"/tracking/vehicles/{vehicleId}/history": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Get a vehicle's location history",
					"operationId": "vehicleLocationHistory",
					"security":    bearerSecurity,
					"parameters":  []any{vehicleIDParameter, limitQueryParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Location history (newest first)",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/VehicleHistoryResponse"}},
							},
						},
						"401": map[string]any{"description": "Authenticated tenant context required"},
						"404": map[string]any{"description": "Not found"},
						"503": map[string]any{"description": "Tracking query not configured"},
					},
				},
			},
			"/tracking/devices/{deviceId}/overview": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Device-detail overview tab",
					"operationId": "deviceOverview",
					"security":    bearerSecurity,
					"parameters":  []any{deviceIDParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Device overview",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/DeviceOverview"}},
							},
						},
						"401": map[string]any{"description": "Authenticated tenant context required"},
						"404": map[string]any{"description": "Not found"},
						"503": map[string]any{"description": "Telemetry query not configured"},
					},
				},
			},
			"/tracking/devices/{deviceId}/connectivity": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Device-detail connectivity tab",
					"operationId": "deviceConnectivity",
					"security":    bearerSecurity,
					"parameters":  []any{deviceIDParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Device connectivity",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/DeviceConnectivity"}},
							},
						},
						"401": map[string]any{"description": "Authenticated tenant context required"},
						"404": map[string]any{"description": "Not found"},
						"503": map[string]any{"description": "Telemetry query not configured"},
					},
				},
			},
			"/tracking/devices/{deviceId}/telemetry": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Device-detail telemetry tab",
					"operationId": "deviceTelemetry",
					"security":    bearerSecurity,
					"parameters":  []any{deviceIDParameter, limitQueryParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Device telemetry with 24h aggregates",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/DeviceTelemetry"}},
							},
						},
						"401": map[string]any{"description": "Authenticated tenant context required"},
						"404": map[string]any{"description": "Not found"},
						"503": map[string]any{"description": "Telemetry query not configured"},
					},
				},
			},
			"/tracking/devices/{deviceId}/firmware": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Device-detail firmware tab",
					"operationId": "deviceFirmware",
					"security":    bearerSecurity,
					"parameters":  []any{deviceIDParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Device firmware history",
							"content": map[string]any{
								"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/DeviceFirmware"}},
							},
						},
						"401": map[string]any{"description": "Authenticated tenant context required"},
						"404": map[string]any{"description": "Not found"},
						"503": map[string]any{"description": "Telemetry query not configured"},
					},
				},
			},
			"/tracking/ws": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tracking"},
					"summary":     "Live tracking WebSocket bridge",
					"description": "Upgrades to a WebSocket. Clients authenticate with a token and receive live location/alert pushes; ingest may also be streamed over the socket. Not a plain HTTP request/response.",
					"operationId": "trackingWebSocket",
					"security":    bearerSecurity,
					"responses": map[string]any{
						"101": map[string]any{"description": "Switching protocols (WebSocket upgrade)"},
						"401": map[string]any{"description": "Authentication required"},
					},
				},
			},
		},
	})
}
