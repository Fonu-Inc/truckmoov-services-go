package httpx

import (
	"strconv"
	"strings"
)

const errorResponseRef = "#/components/schemas/ErrorResponse"

// ErrorResponseSchema documents ErrorBody.
func ErrorResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":     map[string]any{"type": "boolean", "enum": []bool{false}},
			"message":    map[string]any{"type": "string", "description": "The one line a client shows, e.g. job not found."},
			"error":      map[string]any{"type": "string", "description": "HTTP status text, e.g. Not Found."},
			"statusCode": map[string]any{"type": "integer"},
			"errors":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Every problem, when there is more than one."},
		},
		"required": []string{"status", "message", "error", "statusCode"},
	}
}

// Envelope documents SuccessBody around data.
func Envelope(data map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":  map[string]any{"type": "boolean", "enum": []bool{true}},
			"message": map[string]any{"type": "string"},
			"data":    data,
		},
		"required": []string{"status", "message", "data"},
	}
}

// ApplyContract makes a spec say what the service actually sends: every 2xx
// JSON body inside the envelope and every error as ErrorResponse. Specs are
// written body-first and passed through here, so none can document a
// different shape. Docs routes serve documents, not API answers, and are left.
func ApplyContract(spec map[string]any) map[string]any {
	schemas := componentSchemas(spec)
	schemas["ErrorResponse"] = ErrorResponseSchema()
	paths, _ := spec["paths"].(map[string]any)
	for path, item := range paths {
		if strings.Contains(path, "/docs") {
			continue
		}
		operations, _ := item.(map[string]any)
		for _, operation := range operations {
			if op, ok := operation.(map[string]any); ok {
				applyToResponses(op, schemas)
			}
		}
	}
	return spec
}

func applyToResponses(operation map[string]any, schemas map[string]any) {
	responses, _ := operation["responses"].(map[string]any)
	for code, response := range responses {
		status, err := strconv.Atoi(code)
		body, ok := response.(map[string]any)
		if err != nil || !ok {
			continue
		}
		switch {
		case status >= 400:
			setJSONSchema(body, map[string]any{"$ref": errorResponseRef})
		case status >= 200 && status < 300:
			if schema := jsonSchema(body); schema != nil && !isEnvelope(schema, schemas) {
				setJSONSchema(body, Envelope(schema))
			}
		}
	}
}

func isEnvelope(schema map[string]any, schemas map[string]any) bool {
	if ref, ok := schema["$ref"].(string); ok {
		resolved, _ := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		return resolved != nil && isEnvelope(resolved, schemas)
	}
	properties, _ := schema["properties"].(map[string]any)
	status, _ := properties["status"].(map[string]any)
	_, hasData := properties["data"]
	return status["type"] == "boolean" && hasData
}

func jsonSchema(response map[string]any) map[string]any {
	content, _ := response["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	return schema
}

func setJSONSchema(response map[string]any, schema map[string]any) {
	content, _ := response["content"].(map[string]any)
	if content == nil {
		content = map[string]any{}
		response["content"] = content
	}
	content["application/json"] = map[string]any{"schema": schema}
}

func componentSchemas(spec map[string]any) map[string]any {
	components, _ := spec["components"].(map[string]any)
	if components == nil {
		components = map[string]any{}
		spec["components"] = components
	}
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		schemas = map[string]any{}
		components["schemas"] = schemas
	}
	return schemas
}

// ContractViolations lists every documented response that breaks the
// contract, for each service's spec test.
func ContractViolations(spec map[string]any) []string {
	schemas := componentSchemas(spec)
	var violations []string
	paths, _ := spec["paths"].(map[string]any)
	for path, item := range paths {
		if strings.Contains(path, "/docs") {
			continue
		}
		operations, _ := item.(map[string]any)
		for method, operation := range operations {
			op, _ := operation.(map[string]any)
			responses, _ := op["responses"].(map[string]any)
			for code, response := range responses {
				if problem := responseViolation(code, response, schemas); problem != "" {
					violations = append(violations, method+" "+path+" "+code+": "+problem)
				}
			}
		}
	}
	return violations
}

func responseViolation(code string, response any, schemas map[string]any) string {
	status, err := strconv.Atoi(code)
	body, _ := response.(map[string]any)
	if err != nil || body == nil {
		return ""
	}
	schema := jsonSchema(body)
	switch {
	case status >= 400 && (schema == nil || schema["$ref"] != errorResponseRef):
		return "error is not ErrorResponse"
	case status >= 200 && status < 300 && schema != nil && !isEnvelope(schema, schemas):
		return "success is not enveloped"
	}
	return ""
}
