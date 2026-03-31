package httpx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func makeTestBearerToken(secret, tenantID, role string) string {
	payload := fmt.Sprintf(`{"sub":"user-1","email":"ops@example.com","role":"%s","tenantId":"%s","exp":%d}`,
		role,
		tenantID,
		time.Now().Add(time.Hour).Unix(),
	)
	encoded := encodeBase64URL([]byte(payload))
	signature := signTokenValue(encoded, secret)
	return encoded + "." + signature
}

func encodeBase64URL(payload []byte) string {
	const encode = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	output := make([]byte, 0, ((len(payload)+2)/3)*4)
	for i := 0; i < len(payload); i += 3 {
		var chunk uint32
		remaining := len(payload) - i
		chunk = uint32(payload[i]) << 16
		if remaining > 1 {
			chunk |= uint32(payload[i+1]) << 8
		}
		if remaining > 2 {
			chunk |= uint32(payload[i+2])
		}
		output = append(output, encode[(chunk>>18)&0x3F], encode[(chunk>>12)&0x3F])
		if remaining > 1 {
			output = append(output, encode[(chunk>>6)&0x3F])
		}
		if remaining > 2 {
			output = append(output, encode[chunk&0x3F])
		}
	}
	return string(output)
}

func TestWithAuthContextStoresClaimsOnRequestContext(t *testing.T) {
	const secret = "replace-me"
	wrapped := WithAuthContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantID := TenantIDFromRequest(r); tenantID != "tenant-42" {
			t.Fatalf("expected tenant-42, got %q", tenantID)
		}
		claims, ok := ClaimsFromRequest(r)
		if !ok {
			t.Fatal("expected auth claims in request context")
		}
		if claims.Role != "DISPATCHER" {
			t.Fatalf("expected DISPATCHER role, got %q", claims.Role)
		}
		w.WriteHeader(http.StatusNoContent)
	}), secret)

	req := httptest.NewRequest(http.MethodGet, "/dispatch/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+makeTestBearerToken(secret, "tenant-42", "DISPATCHER"))
	res := httptest.NewRecorder()

	wrapped.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, res.Code)
	}
}
