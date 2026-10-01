package httpx

import "testing"

func TestApplyContractWrapsSuccessAndErrors(t *testing.T) {
	spec := map[string]any{
		"paths": map[string]any{
			"/things": map[string]any{
				"get": map[string]any{
					"responses": map[string]any{
						"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "array"}}}},
						"404": map[string]any{"description": "Not found"},
					},
				},
			},
			"/things/docs": map[string]any{
				"get": map[string]any{
					"responses": map[string]any{
						"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
					},
				},
			},
		},
	}

	if violations := ContractViolations(spec); len(violations) != 2 {
		t.Fatalf("expected the raw success and the bare error flagged, got %v", violations)
	}
	ApplyContract(spec)
	if violations := ContractViolations(spec); len(violations) != 0 {
		t.Fatalf("expected a compliant spec, got %v", violations)
	}
}

func TestApplyContractLeavesAnEnvelopeAlone(t *testing.T) {
	envelope := Envelope(map[string]any{"type": "object"})
	spec := map[string]any{
		"components": map[string]any{"schemas": map[string]any{"JobResponse": envelope}},
		"paths": map[string]any{
			"/jobs": map[string]any{
				"get": map[string]any{
					"responses": map[string]any{
						"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/JobResponse"}}}},
					},
				},
			},
		},
	}

	ApplyContract(spec)

	schema := jsonSchema(spec["paths"].(map[string]any)["/jobs"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any))
	if schema["$ref"] != "#/components/schemas/JobResponse" {
		t.Fatalf("expected the envelope ref kept, got %v", schema)
	}
}
