package httpx

import (
	"encoding/json"
	"net/http"
)

// The response contract every TruckMoov service answers with, TS and Go alike.
// Handlers write through these functions only; contract_test.go fails the
// build when one writes JSON any other way.

type SuccessBody struct {
	Status  bool      `json:"status"`
	Message string    `json:"message"`
	Data    any       `json:"data"`
	Meta    *PageMeta `json:"meta,omitempty"`
}

type ErrorBody struct {
	Status     bool     `json:"status"`
	Message    string   `json:"message"`
	Error      string   `json:"error"`
	StatusCode int      `json:"statusCode"`
	Errors     []string `json:"errors,omitempty"`
}

func WriteOK(w http.ResponseWriter, status int, data any) {
	WriteSuccess(w, status, "success", data)
}

func WritePage(w http.ResponseWriter, status int, data any, meta PageMeta) {
	writeJSON(w, status, SuccessBody{Status: true, Message: "success", Data: data, Meta: &meta})
}

func WriteSuccess(w http.ResponseWriter, status int, message string, data any) {
	writeJSON(w, status, SuccessBody{Status: true, Message: message, Data: data})
}

// WriteError answers with message as the one line a client shows. details,
// when given, list every problem, e.g. each failed validation.
func WriteError(w http.ResponseWriter, status int, message string, details ...string) {
	writeJSON(w, status, ErrorBody{
		Status:     false,
		Message:    message,
		Error:      http.StatusText(status),
		StatusCode: status,
		Errors:     details,
	})
}

// NotFound is the answer for an unknown path and for every refusal that must
// look like one, so the two cannot be told apart.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	WriteError(w, http.StatusNotFound, "not found")
}

// WriteDocument serves a document as-is, outside the envelope. Only for
// machine-read documents such as an OpenAPI spec.
func WriteDocument(w http.ResponseWriter, document any) {
	writeJSON(w, http.StatusOK, document)
}

// WriteHTML serves a page, such as the Swagger UI, outside the envelope.
func WriteHTML(w http.ResponseWriter, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(page))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
