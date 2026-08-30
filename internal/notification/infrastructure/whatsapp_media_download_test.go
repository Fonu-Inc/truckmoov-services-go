package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetaWhatsAppMessageProviderDownloadMedia_Success(t *testing.T) {
	t.Helper()

	var gotMediaLookupAuth, gotBlobAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media-1":
			gotMediaLookupAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"url":"http://` + r.Host + `/media-blob","mime_type":"image/jpeg","file_size":9}`))
		case "/media-blob":
			gotBlobAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("jpeg-data"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	provider := NewMetaWhatsAppMessageProviderWithClient(server.URL, nil, nil, "123456", "access-token", server.Client())

	data, contentType, err := provider.DownloadMedia(context.Background(), "tenant-a", "media-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(data) != "jpeg-data" {
		t.Fatalf("expected downloaded bytes, got %q", data)
	}
	if contentType != "image/jpeg" {
		t.Fatalf("expected content type from response header, got %s", contentType)
	}
	if gotMediaLookupAuth != "Bearer access-token" {
		t.Fatalf("expected media lookup to carry bearer token, got %q", gotMediaLookupAuth)
	}
	if gotBlobAuth != "Bearer access-token" {
		t.Fatalf("expected media download to carry the same bearer token, got %q", gotBlobAuth)
	}
}

func TestMetaWhatsAppMessageProviderDownloadMedia_RequiresMediaID(t *testing.T) {
	provider := NewMetaWhatsAppMessageProviderWithClient("http://example.invalid", nil, nil, "123456", "access-token", nil)

	if _, _, err := provider.DownloadMedia(context.Background(), "tenant-a", "  "); err == nil {
		t.Fatal("expected an error for an empty media id")
	}
}

func TestMetaWhatsAppMessageProviderDownloadMedia_PropagatesLookupFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"media not found"}}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppMessageProviderWithClient(server.URL, nil, nil, "123456", "access-token", server.Client())

	if _, _, err := provider.DownloadMedia(context.Background(), "tenant-a", "missing"); err == nil {
		t.Fatal("expected an error when the media lookup fails")
	}
}
