package identity

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testIssuer = "https://kubernetes.default.svc.cluster.local"

func clusterToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	payload, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func clusterVerifier(t *testing.T, self string) (*ServiceTokenVerifier, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	published := jwk{
		Kid: "k1",
		Kty: "RSA",
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
	v := &ServiceTokenVerifier{self: self, namespace: "truckmoov", now: time.Now, keys: &clusterKeys{
		fetch: func() (string, []jwk, error) { return testIssuer, []jwk{published}, nil },
	}}
	return v, key
}

func claimsFor(caller, audience string) map[string]any {
	return map[string]any{
		"iss": testIssuer,
		"sub": "system:serviceaccount:truckmoov:" + caller,
		"aud": []string{audience},
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

func TestClusterTokenNamesTheCallingService(t *testing.T) {
	v, key := clusterVerifier(t, "fleet")

	caller, err := v.Verify(clusterToken(t, key, "k1", claimsFor("dispatch", "fleet")))
	if err != nil || caller.Service != "dispatch" {
		t.Fatalf("expected dispatch, got %+v %v", caller, err)
	}
}

// Without this pod's own namespace no cluster caller can be placed, so none is accepted.
func TestClusterTokenRefusedWhenOwnNamespaceUnknown(t *testing.T) {
	v, key := clusterVerifier(t, "fleet")
	v.namespace = ""

	if _, err := v.Verify(clusterToken(t, key, "k1", claimsFor("dispatch", "fleet"))); err == nil {
		t.Fatal("expected refusal when this pod's namespace is unknown")
	}
}

// A token is bound to the service it was issued for, so one taken from a
// call to notification cannot be replayed against fleet.
func TestClusterTokenForAnotherServiceIsRefused(t *testing.T) {
	v, key := clusterVerifier(t, "fleet")

	if _, err := v.Verify(clusterToken(t, key, "k1", claimsFor("dispatch", "notification"))); err == nil {
		t.Fatal("expected a token addressed to another service to be refused")
	}
}

func TestClusterTokenRefusals(t *testing.T) {
	v, key := clusterVerifier(t, "fleet")
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	expired := claimsFor("dispatch", "fleet")
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	wrongIssuer := claimsFor("dispatch", "fleet")
	wrongIssuer["iss"] = "https://elsewhere"
	notAService := claimsFor("dispatch", "fleet")
	notAService["sub"] = "someone"
	otherNamespace := claimsFor("dispatch", "fleet")
	otherNamespace["sub"] = "system:serviceaccount:intruder:dispatch"

	cases := map[string]string{
		"expired":         clusterToken(t, key, "k1", expired),
		"another issuer":  clusterToken(t, key, "k1", wrongIssuer),
		"another key":     clusterToken(t, other, "k1", claimsFor("dispatch", "fleet")),
		"unknown key":     clusterToken(t, key, "k9", claimsFor("dispatch", "fleet")),
		"not a service":   clusterToken(t, key, "k1", notAService),
		"other namespace": clusterToken(t, key, "k1", otherNamespace),
		"garbage":         "a.b.c",
	}
	for name, token := range cases {
		if _, err := v.Verify(token); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
}

func TestDevTokensWorkOnlyOutsideTheCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	token := NewServiceTokens("dispatch", t.TempDir(), "dev-secret").For("fleet")

	caller, err := NewServiceTokenVerifier("fleet", "dev-secret").Verify(token)
	if err != nil || caller.Service != "dispatch" {
		t.Fatalf("expected dispatch outside the cluster, got %+v %v", caller, err)
	}

	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	if NewServiceTokens("dispatch", t.TempDir(), "dev-secret").For("fleet") != "" {
		t.Fatal("a pod in the cluster must not sign its own service tokens")
	}
	inCluster := NewServiceTokenVerifier("fleet", "dev-secret")
	inCluster.keys = &clusterKeys{fetch: func() (string, []jwk, error) { return testIssuer, nil, nil }}
	if _, err := inCluster.Verify(token); err == nil {
		t.Fatal("a dev token must be refused inside the cluster")
	}
}

func TestProjectedTokenIsReadFromItsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "fleet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fleet", "token"), []byte("projected\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := NewServiceTokens("dispatch", dir, "").For("fleet"); got != "projected" {
		t.Fatalf("expected the projected token, got %q", got)
	}
	if got := NewServiceTokens("dispatch", dir, "").For("auth"); got != "" {
		t.Fatalf("expected no token for a service with none mounted, got %q", got)
	}
}
