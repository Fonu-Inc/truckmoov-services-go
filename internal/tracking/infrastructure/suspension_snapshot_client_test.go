package infrastructure

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/truckmoov/services-go/pkg/identity"
)

func snapshotCaller(t *testing.T) *identity.Caller {
	dir := t.TempDir()
	for _, callee := range []string{"auth", "fleet"} {
		_ = os.MkdirAll(filepath.Join(dir, callee), 0o755)
		_ = os.WriteFile(filepath.Join(dir, callee, "token"), []byte("tok"), 0o600)
	}
	return identity.NewCaller("tracking", identity.NewServiceTokens("tracking", dir, ""), "")
}

func TestSuspensionSnapshotClient_LoadsTenantsAndPagedDevices(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"tenants":[{"tenantId":"t1"},{"tenantId":"t2"}]}}`)
	}))
	defer auth.Close()
	fleet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(identity.ServiceTokenHeader) == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"data":{"devices":[{"imei":"111"}],"nextCursor":"c2"}}`)
			return
		}
		fmt.Fprint(w, `{"devices":[{"imei":"222"}],"nextCursor":null}`)
	}))
	defer fleet.Close()

	snapshot, err := NewSuspensionSnapshotClient(auth.URL, fleet.URL, snapshotCaller(t)).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tenants) != 2 || len(snapshot.IMEIs) != 2 {
		t.Fatalf("got %+v", snapshot)
	}
}

func TestSuspensionSnapshotClient_ErrorsWhenEitherSourceFails(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"tenants":[],"devices":[]}}`)
	}))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()

	for name, urls := range map[string][2]string{"auth down": {down.URL, ok.URL}, "fleet down": {ok.URL, down.URL}} {
		if _, err := NewSuspensionSnapshotClient(urls[0], urls[1], snapshotCaller(t)).Load(context.Background()); err == nil {
			t.Fatalf("%s: expected error so the old lists stay", name)
		}
	}
}
