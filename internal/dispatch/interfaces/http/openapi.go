package dispatchhttp

import "net/http"

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Dispatch Service API Docs</title>
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
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(swaggerUIHTML))
}

func (h *Handler) docsOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	respondJSON(w, http.StatusOK, dispatchOpenAPISpec())
}

func dispatchOpenAPISpec() map[string]any {
	jobIDParameter := map[string]any{
		"name":     "jobId",
		"in":       "path",
		"required": true,
		"schema": map[string]any{
			"type": "string",
		},
	}
	requestIDParameter := map[string]any{
		"name":     "requestId",
		"in":       "path",
		"required": true,
		"schema": map[string]any{
			"type": "string",
		},
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Dispatch Service API",
			"version":     "0.1.0",
			"description": "Dispatch lifecycle API for Epic D (job creation, assignment, execution, POD, timeline).",
		},
		"servers": []map[string]any{
			{"url": "/"},
		},
		"tags": []map[string]any{
			{"name": "dispatch", "description": "Dispatch service endpoints"},
		},
		"paths": map[string]any{
			"/dispatch/health": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Health check",
					"operationId": "dispatchHealth",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Service is healthy",
						},
					},
				},
			},
			"/dispatch/eta": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Compute ETA",
					"description": "Computes ETA for the authenticated tenant.",
					"operationId": "computeETA",
					"parameters": []map[string]any{
						{"name": "job_id", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "vehicle_id", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "destination", "in": "query", "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "ETA response",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"eta_seconds":           map[string]any{"type": "integer"},
											"distance_km":           map[string]any{"type": "number"},
											"planned_completion_at": map[string]any{"type": "string", "format": "date-time"},
											"source":                map[string]any{"type": "string"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/routes/suggest": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Suggest an optimal route for a job",
					"operationId": "suggestRoute",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/SuggestRouteRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Route plan",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/RoutePlan"},
								},
							},
						},
					},
				},
			},
			"/dispatch/routes/optimize": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Optimize a multi-stop route across jobs",
					"operationId": "optimizeRoute",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/OptimizeRouteRequest"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Optimized route plan",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/RoutePlan"},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/status": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Publish job status event (legacy compatibility)",
					"operationId": "publishJobStatusLegacy",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/LegacyStatusRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"202": map[string]any{
							"description": "Status event published",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/EventPublishedResponse",
									},
								},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/ErrorResponse",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "List jobs",
					"operationId": "listJobs",
					"description": "Returns dispatch jobs for the authenticated tenant. Supports simple filtering for the Dispatch Center page.",
					"parameters": []map[string]any{
						{"name": "status", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"all", "pending", "created", "assigned", "in_progress", "en-route", "arrived", "loaded", "completed", "delivered", "cancelled"}}},
						{"name": "query", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "driver_id", "in": "query", "schema": map[string]any{"type": "string"}},
						{"name": "vehicle_id", "in": "query", "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Jobs list",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/JobListResponse",
									},
								},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
								},
							},
						},
					},
				},
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Create job from the dispatch UI payload",
					"operationId": "createJob",
					"description": "Accepts the original Dispatch Center create-dispatch modal payload. The older richer flat payload and nested pickup/dropoff payload are still accepted for backward compatibility.",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/CreateJobRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Job created",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/ErrorResponse",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Get job details",
					"operationId": "getJob",
					"parameters":  []any{jobIDParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Job details",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/JobResponse",
									},
								},
							},
						},
						"404": map[string]any{
							"description": "Job not found",
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/scheduling": map[string]any{
				"patch": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Update job priority/time windows",
					"operationId": "updateJobScheduling",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/UpdateJobSchedulingRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Job scheduling updated",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
						},
						"404": map[string]any{
							"description": "Job not found",
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/assign": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Assign or reassign a job to driver/vehicle",
					"operationId": "assignJob",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/AssignJobRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Job assigned",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/cancel": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Cancel a job with a reason",
					"operationId": "cancelJob",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/CancelJobRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Job cancelled",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/acknowledge": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Driver acknowledges job instruction",
					"operationId": "acknowledgeJob",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/AcknowledgeJobRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Acknowledged",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/status": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Update driver job status",
					"operationId": "updateJobStatus",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/UpdateJobStatusRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Status updated",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/send-whatsapp-instruction": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Send WhatsApp job instruction to driver",
					"description": "Allowed only while the job is in assigned status, and only for the driver currently assigned to the job.",
					"operationId": "sendWhatsAppInstruction",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/SendJobInstructionRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Driver instruction sent",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/JobResponse",
									},
								},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
								},
							},
						},
						"404": map[string]any{
							"description": "Job not found",
						},
						"502": map[string]any{
							"description": "Notification delivery failed",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/driver-status-requests": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Record a driver-reported status request",
					"description": "Creates a pending driver status request from a recognized WhatsApp keyword reply. This does not update the job status until a dispatcher confirms it.",
					"operationId": "createDriverStatusRequest",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/CreateDriverStatusRequestRequest"},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Pending driver status request created",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/DriverStatusRequestActionResponse"},
								},
							},
						},
						"400": map[string]any{
							"description": "Validation error",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
								},
							},
						},
						"404": map[string]any{"description": "Job not found"},
						"409": map[string]any{
							"description": "Status request conflicts with the current job state",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/driver-status-requests/{requestId}/confirm": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Confirm a driver-reported status request",
					"description": "Dispatcher-capable roles confirm the pending request, and only then is the actual job status updated.",
					"operationId": "confirmDriverStatusRequest",
					"parameters":  []any{jobIDParameter, requestIDParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Driver status request confirmed and job status updated",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/DriverStatusRequestActionResponse"},
								},
							},
						},
						"404": map[string]any{"description": "Job or driver status request not found"},
						"409": map[string]any{
							"description": "Confirmation conflicts with the current job state",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/timeline": map[string]any{
				"get": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Get job timeline",
					"operationId": "getJobTimeline",
					"parameters":  []any{jobIDParameter},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Timeline response",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/JobTimelineResponse",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/pod-attachments": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Add proof-of-delivery attachment",
					"operationId": "addJobPODAttachment",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/AddPODAttachmentRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "POD attachment added",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
					},
				},
			},
			"/dispatch/jobs/{jobId}/notes": map[string]any{
				"post": map[string]any{
					"tags":        []string{"dispatch"},
					"summary":     "Add dispatcher note to a job",
					"operationId": "addJobNote",
					"parameters":  []any{jobIDParameter},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"$ref": "#/components/schemas/AddJobNoteRequest",
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Note added",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"$ref": "#/components/schemas/Job",
									},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"Stop": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address":       map[string]any{"type": "string"},
						"contact_name":  map[string]any{"type": "string"},
						"contact_phone": map[string]any{"type": "string"},
						"latitude":      map[string]any{"type": "number"},
						"longitude":     map[string]any{"type": "number"},
						"notes":         map[string]any{"type": "string"},
					},
					"required": []string{"address"},
				},
				"JobTimeWindow": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pickup_start":  map[string]any{"type": "string", "format": "date-time"},
						"pickup_end":    map[string]any{"type": "string", "format": "date-time"},
						"dropoff_start": map[string]any{"type": "string", "format": "date-time"},
						"dropoff_end":   map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"CreateJobRequest": map[string]any{
					"type":        "object",
					"description": "Preferred request shape for the original Dispatch Center modal, with legacy field aliases still supported.",
					"properties": map[string]any{
						"job_id":                    map[string]any{"type": "string", "description": "Optional internal job identifier. The service generates one when omitted."},
						"reference":                 map[string]any{"type": "string", "description": "Business-visible job reference shown in the UI."},
						"vehicleId":                 map[string]any{"type": "string", "description": "Selected vehicle from the create-dispatch modal."},
						"vehicle_id":                map[string]any{"type": "string", "description": "Legacy alias for vehicleId."},
						"driverId":                  map[string]any{"type": "string", "description": "Optional driver selected in the create-dispatch modal."},
						"driver_id":                 map[string]any{"type": "string", "description": "Legacy alias for driverId."},
						"driverPhone":               map[string]any{"type": "string", "description": "Optional WhatsApp phone for the selected driver. Persisted when the job is created already assigned."},
						"driver_phone":              map[string]any{"type": "string", "description": "Legacy alias for driverPhone."},
						"pickupLocation":            map[string]any{"type": "string"},
						"pickupAddress":             map[string]any{"type": "string"},
						"pickupLat":                 map[string]any{"type": "number"},
						"pickupLng":                 map[string]any{"type": "number"},
						"deliveryLocation":          map[string]any{"type": "string"},
						"deliveryAddress":           map[string]any{"type": "string"},
						"deliveryLat":               map[string]any{"type": "number"},
						"deliveryLng":               map[string]any{"type": "number"},
						"cargoType":                 map[string]any{"type": "string"},
						"cargoWeight":               map[string]any{"type": "number", "description": "Cargo weight in tons, matching the UI form."},
						"cargoDescription":          map[string]any{"type": "string"},
						"notes":                     map[string]any{"type": "string"},
						"additionalNotes":           map[string]any{"type": "string", "description": "Legacy alias for notes."},
						"amount":                    map[string]any{"type": "number", "description": "Quoted amount in NGN."},
						"deadline":                  map[string]any{"type": "string", "format": "date-time", "description": "Accepts RFC3339 and HTML datetime-local values."},
						"required_vehicle_type":     map[string]any{"type": "string"},
						"load_weight_kg":            map[string]any{"type": "number"},
						"load_volume_m3":            map[string]any{"type": "number"},
						"estimated_service_minutes": map[string]any{"type": "integer"},
						"priority":                  map[string]any{"type": "string", "enum": []string{"low", "normal", "high", "urgent"}},
						"time_window":               map[string]any{"$ref": "#/components/schemas/JobTimeWindow"},
						"pickup":                    map[string]any{"$ref": "#/components/schemas/Stop"},
						"dropoff":                   map[string]any{"$ref": "#/components/schemas/Stop"},
					},
					"required": []string{"vehicleId", "pickupLocation", "deliveryLocation"},
				},
				"RouteLocation": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"label":     map[string]any{"type": "string"},
						"address":   map[string]any{"type": "string"},
						"latitude":  map[string]any{"type": "number"},
						"longitude": map[string]any{"type": "number"},
					},
					"required": []string{"latitude", "longitude"},
				},
				"VehicleProfile": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"vehicle_id":         map[string]any{"type": "string"},
						"vehicle_type":       map[string]any{"type": "string"},
						"capacity_weight_kg": map[string]any{"type": "number"},
						"capacity_volume_m3": map[string]any{"type": "number"},
						"average_speed_kph":  map[string]any{"type": "number"},
					},
				},
				"DriverShift": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"start": map[string]any{"type": "string", "format": "date-time"},
						"end":   map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"SuggestRouteRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id":         map[string]any{"type": "string"},
						"origin":         map[string]any{"$ref": "#/components/schemas/RouteLocation"},
						"vehicle":        map[string]any{"$ref": "#/components/schemas/VehicleProfile"},
						"driver_shift":   map[string]any{"$ref": "#/components/schemas/DriverShift"},
						"departure_time": map[string]any{"type": "string", "format": "date-time"},
					},
					"required": []string{"job_id", "origin", "vehicle"},
				},
				"OptimizeRouteRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"origin":         map[string]any{"$ref": "#/components/schemas/RouteLocation"},
						"vehicle":        map[string]any{"$ref": "#/components/schemas/VehicleProfile"},
						"driver_shift":   map[string]any{"$ref": "#/components/schemas/DriverShift"},
						"departure_time": map[string]any{"type": "string", "format": "date-time"},
					},
					"required": []string{"job_ids", "origin", "vehicle"},
				},
				"RouteStep": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"sequence":                  map[string]any{"type": "integer"},
						"job_id":                    map[string]any{"type": "string"},
						"stop_type":                 map[string]any{"type": "string"},
						"address":                   map[string]any{"type": "string"},
						"label":                     map[string]any{"type": "string"},
						"latitude":                  map[string]any{"type": "number"},
						"longitude":                 map[string]any{"type": "number"},
						"distance_from_previous_km": map[string]any{"type": "number"},
						"travel_seconds_from_prev":  map[string]any{"type": "integer"},
						"arrival_at":                map[string]any{"type": "string", "format": "date-time"},
						"departure_at":              map[string]any{"type": "string", "format": "date-time"},
						"eta_seconds":               map[string]any{"type": "integer"},
					},
				},
				"RoutePlan": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"feasible":               map[string]any{"type": "boolean"},
						"infeasibility_reasons":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"vehicle_id":             map[string]any{"type": "string"},
						"vehicle_type":           map[string]any{"type": "string"},
						"departure_time":         map[string]any{"type": "string", "format": "date-time"},
						"completion_time":        map[string]any{"type": "string", "format": "date-time"},
						"eta_seconds":            map[string]any{"type": "integer"},
						"total_distance_km":      map[string]any{"type": "number"},
						"total_duration_seconds": map[string]any{"type": "integer"},
						"ordered_steps":          map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/RouteStep"}},
					},
				},
				"UpdateJobSchedulingRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"priority":    map[string]any{"type": "string", "enum": []string{"low", "normal", "high", "urgent"}},
						"time_window": map[string]any{"$ref": "#/components/schemas/JobTimeWindow"},
					},
				},
				"AssignJobRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"driver_id":    map[string]any{"type": "string"},
						"driver_phone": map[string]any{"type": "string", "description": "Persisted WhatsApp phone for the assigned driver."},
						"driverPhone":  map[string]any{"type": "string", "description": "CamelCase alias for driver_phone."},
						"vehicle_id":   map[string]any{"type": "string"},
						"reason":       map[string]any{"type": "string"},
					},
					"required": []string{"driver_id", "vehicle_id"},
				},
				"CancelJobRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{"type": "string"},
					},
					"required": []string{"reason"},
				},
				"AcknowledgeJobRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"driver_id": map[string]any{"type": "string"},
					},
				},
				"UpdateJobStatusRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": map[string]any{
							"type": "string",
							"enum": []string{"created", "assigned", "en-route", "arrived", "loaded", "delivered", "cancelled"},
						},
					},
					"required": []string{"status"},
				},
				"AddPODAttachmentRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":        map[string]any{"type": "string"},
						"url":         map[string]any{"type": "string"},
						"uploaded_by": map[string]any{"type": "string"},
					},
					"required": []string{"kind", "url"},
				},
				"SendJobInstructionHeaderMedia": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":     map[string]any{"type": "string", "enum": []string{"image", "video", "document"}},
						"link":     map[string]any{"type": "string"},
						"id":       map[string]any{"type": "string"},
						"filename": map[string]any{"type": "string"},
					},
				},
				"SendJobInstructionButtonParameter": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index":   map[string]any{"type": "integer"},
						"type":    map[string]any{"type": "string", "enum": []string{"quick_reply", "url", "copy_code"}},
						"text":    map[string]any{"type": "string"},
						"payload": map[string]any{"type": "string"},
					},
					"required": []string{"index"},
				},
				"SendJobInstructionRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"driver_id":     map[string]any{"type": "string", "description": "Assigned driver id. Must match the job's assigned_driver_id."},
						"driverId":      map[string]any{"type": "string", "description": "CamelCase alias for driver_id."},
						"to":            map[string]any{"type": "string", "description": "Optional compatibility field. If provided, it must match the persisted assigned_driver_phone."},
						"recipient":     map[string]any{"type": "string", "description": "Legacy alias for to."},
						"template_name": map[string]any{"type": "string", "description": "Approved WhatsApp job_assignment template name."},
						"templateName":  map[string]any{"type": "string", "description": "CamelCase alias for template_name."},
						"language":      map[string]any{"type": "string", "description": "Optional template language override. Defaults to the template language."},
						"driver_name":   map[string]any{"type": "string", "description": "Driver display name inserted into the template variable set."},
						"driverName":    map[string]any{"type": "string", "description": "CamelCase alias for driver_name."},
						"header_media":  map[string]any{"$ref": "#/components/schemas/SendJobInstructionHeaderMedia"},
						"headerMedia":   map[string]any{"$ref": "#/components/schemas/SendJobInstructionHeaderMedia"},
						"button_parameters": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/SendJobInstructionButtonParameter"},
						},
						"buttonParameters": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/SendJobInstructionButtonParameter"},
						},
					},
					"required": []string{"driver_id", "template_name", "driver_name"},
				},
				"CreateDriverStatusRequestRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"driver_id":         map[string]any{"type": "string", "description": "Driver reporting the status. Must match the job's assigned_driver_id."},
						"driverId":          map[string]any{"type": "string", "description": "CamelCase alias for driver_id."},
						"driver_phone":      map[string]any{"type": "string", "description": "Driver WhatsApp number for compatibility checks against the stored assigned_driver_phone."},
						"driverPhone":       map[string]any{"type": "string", "description": "CamelCase alias for driver_phone."},
						"keyword":           map[string]any{"type": "string", "description": "Recognized driver keyword. Supported values: ENROUTE, ARRIVED, LOADED, DELIVERED."},
						"raw_message":       map[string]any{"type": "string"},
						"rawMessage":        map[string]any{"type": "string", "description": "CamelCase alias for raw_message."},
						"source_message_id": map[string]any{"type": "string", "description": "Inbound WhatsApp provider message id that triggered the request."},
						"sourceMessageID":   map[string]any{"type": "string", "description": "CamelCase alias for source_message_id."},
						"channel":           map[string]any{"type": "string", "description": "Origin channel. Defaults to whatsapp."},
					},
					"required": []string{"driver_id", "keyword"},
				},
				"AddJobNoteRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":      map[string]any{"type": "string"},
						"author_id": map[string]any{"type": "string"},
					},
					"required": []string{"text"},
				},
				"LegacyStatusRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id":          map[string]any{"type": "string"},
						"previous_status": map[string]any{"type": "string"},
						"new_status":      map[string]any{"type": "string"},
						"vehicle_id":      map[string]any{"type": "string"},
						"driver_id":       map[string]any{"type": "string"},
						"reason":          map[string]any{"type": "string"},
					},
					"required": []string{"job_id", "new_status"},
				},
				"EventPublishedResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"published": map[string]any{"type": "boolean"},
						"subject":   map[string]any{"type": "string"},
						"event_id":  map[string]any{"type": "string"},
					},
				},
				"JobTimelineEntry": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string"},
						"type":        map[string]any{"type": "string"},
						"summary":     map[string]any{"type": "string"},
						"occurred_at": map[string]any{"type": "string", "format": "date-time"},
						"metadata": map[string]any{
							"type":                 "object",
							"additionalProperties": map[string]any{"type": "string"},
						},
					},
				},
				"JobNote": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"text":       map[string]any{"type": "string"},
						"author_id":  map[string]any{"type": "string"},
						"created_at": map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"JobPODAttachment": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string"},
						"kind":        map[string]any{"type": "string"},
						"url":         map[string]any{"type": "string"},
						"uploaded_by": map[string]any{"type": "string"},
						"uploaded_at": map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"DriverStatusRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                map[string]any{"type": "string"},
						"requested_status":  map[string]any{"type": "string", "enum": []string{"en-route", "arrived", "loaded", "delivered"}},
						"keyword":           map[string]any{"type": "string"},
						"channel":           map[string]any{"type": "string"},
						"driver_id":         map[string]any{"type": "string"},
						"driver_phone":      map[string]any{"type": "string"},
						"raw_message":       map[string]any{"type": "string"},
						"source_message_id": map[string]any{"type": "string"},
						"state":             map[string]any{"type": "string", "enum": []string{"pending", "confirmed"}},
						"requested_at":      map[string]any{"type": "string", "format": "date-time"},
						"confirmed_at":      map[string]any{"type": "string", "format": "date-time"},
						"confirmed_by":      map[string]any{"type": "string"},
					},
				},
				"Job": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":                        map[string]any{"type": "string"},
						"reference":                 map[string]any{"type": "string"},
						"pickup":                    map[string]any{"$ref": "#/components/schemas/Stop"},
						"dropoff":                   map[string]any{"$ref": "#/components/schemas/Stop"},
						"cargo_type":                map[string]any{"type": "string"},
						"cargo_description":         map[string]any{"type": "string"},
						"amount_ngn":                map[string]any{"type": "number"},
						"deadline":                  map[string]any{"type": "string", "format": "date-time"},
						"required_vehicle_type":     map[string]any{"type": "string"},
						"load_weight_kg":            map[string]any{"type": "number"},
						"load_volume_m3":            map[string]any{"type": "number"},
						"estimated_service_minutes": map[string]any{"type": "integer"},
						"priority":                  map[string]any{"type": "string", "enum": []string{"low", "normal", "high", "urgent"}},
						"time_window":               map[string]any{"$ref": "#/components/schemas/JobTimeWindow"},
						"status":                    map[string]any{"type": "string"},
						"assigned_driver_id":        map[string]any{"type": "string"},
						"assigned_driver_phone":     map[string]any{"type": "string"},
						"assigned_vehicle_id":       map[string]any{"type": "string"},
						"cancellation_reason":       map[string]any{"type": "string"},
						"acknowledged_at":           map[string]any{"type": "string", "format": "date-time"},
						"created_at":                map[string]any{"type": "string", "format": "date-time"},
						"updated_at":                map[string]any{"type": "string", "format": "date-time"},
						"timeline": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/JobTimelineEntry"},
						},
						"notes": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/JobNote"},
						},
						"pod_attachments": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/JobPODAttachment"},
						},
						"driver_status_requests": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/DriverStatusRequest"},
						},
					},
				},
				"JobResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/Job"},
					},
				},
				"JobTimelinePayload": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id": map[string]any{"type": "string"},
						"timeline": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/JobTimelineEntry"},
						},
					},
				},
				"JobTimelineResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/JobTimelinePayload"},
					},
				},
				"JobListResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/Job"},
						},
					},
				},
				"DriverStatusRequestActionPayload": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job":     map[string]any{"$ref": "#/components/schemas/Job"},
						"request": map[string]any{"$ref": "#/components/schemas/DriverStatusRequest"},
					},
				},
				"DriverStatusRequestActionResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status":  map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
						"data":    map[string]any{"$ref": "#/components/schemas/DriverStatusRequestActionPayload"},
					},
				},
				"ErrorResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"error": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}
