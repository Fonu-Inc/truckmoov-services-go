package trackinghttp

import (
	"testing"

	httpx "github.com/truckmoov/services-go/pkg/http"
)

func TestSpecDocumentsTheResponseContract(t *testing.T) {
	if violations := httpx.ContractViolations(trackingOpenAPISpec()); len(violations) > 0 {
		t.Fatalf("spec breaks the response contract:\n%v", violations)
	}
}
