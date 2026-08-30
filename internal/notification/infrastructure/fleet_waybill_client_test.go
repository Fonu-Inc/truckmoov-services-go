package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetWaybillClient_CaptureWaybillFromWhatsApp_Success(t *testing.T) {
	var gotAuthorization, gotQuery, gotContentType string
	var gotReplyAddress string
	var gotFileBytes []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		gotContentType = r.Header.Get("Content-Type")

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		gotReplyAddress = r.FormValue("replyAddress")
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("read form file: %v", err)
		}
		defer file.Close()
		buf := make([]byte, 1024)
		n, _ := file.Read(buf)
		gotFileBytes = buf[:n]

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":true}`))
	}))
	defer server.Close()

	client := NewFleetWaybillClient(server.URL, "test-secret", server.Client())

	err := client.CaptureWaybillFromWhatsApp(context.Background(), "tenant-a", []byte("jpeg-bytes"), "media-1.jpg", "image/jpeg", "2348012345678")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if gotQuery != "source=whatsapp" {
		t.Fatalf("expected source=whatsapp query, got %q", gotQuery)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Fatalf("expected multipart content type, got %q", gotContentType)
	}
	if gotReplyAddress != "2348012345678" {
		t.Fatalf("expected reply address forwarded, got %q", gotReplyAddress)
	}
	if string(gotFileBytes) != "jpeg-bytes" {
		t.Fatalf("expected image bytes forwarded, got %q", gotFileBytes)
	}
	if !strings.HasPrefix(gotAuthorization, "Bearer ") {
		t.Fatalf("expected a bearer token, got %q", gotAuthorization)
	}

	// The token is HMAC-signed with the shared secret, not opaque — a
	// tenant-scoped claim inside it is what fleet's auth middleware trusts.
	token := strings.TrimPrefix(gotAuthorization, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("expected a two-part signed token, got %d parts", len(parts))
	}
}

func TestFleetWaybillClient_CaptureWaybillFromWhatsApp_PropagatesFleetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"a waybill image is required"}`))
	}))
	defer server.Close()

	client := NewFleetWaybillClient(server.URL, "test-secret", server.Client())

	err := client.CaptureWaybillFromWhatsApp(context.Background(), "tenant-a", []byte("x"), "x.jpg", "image/jpeg", "2348012345678")
	if err == nil {
		t.Fatal("expected an error when fleet refuses the upload")
	}
}

func TestFleetWaybillClient_CaptureWaybillFromWhatsApp_RequiresBaseURL(t *testing.T) {
	client := NewFleetWaybillClient("", "test-secret", nil)

	err := client.CaptureWaybillFromWhatsApp(context.Background(), "tenant-a", []byte("x"), "x.jpg", "image/jpeg", "2348012345678")
	if err == nil {
		t.Fatal("expected an error when fleet's url is not configured")
	}
}
