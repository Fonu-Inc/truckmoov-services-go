package dispatchhttp

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const (
	ownerOperatorRole             = "OWNER_OPERATOR"
	ownerOperatorOrganisationType = "owner_operator"
)

type ownedVehicleLister interface {
	OwnedVehicleIDs(ctx context.Context, tenantID, ownerOperatorID string) ([]string, error)
}

// limitedOwnerOperatorID is the owner operator whose vehicles and own jobs the
// caller is limited to. In an owner_operator organisation the role is the founder, who
// runs the whole fleet; anywhere else it is an operator brought in, who sees
// only their own vehicles. An unknown organisation type counts as the second,
// so not knowing narrows access rather than widening it.
func limitedOwnerOperatorID(r *http.Request) (string, bool) {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || !strings.EqualFold(strings.TrimSpace(claims.Role), ownerOperatorRole) {
		return "", false
	}
	if claims.OrganizationType == ownerOperatorOrganisationType {
		return "", false
	}
	return strings.TrimSpace(claims.Subject), true
}

// jobScopeFor is nil for a caller who sees the whole tenant. A failed lookup
// leaves an owner operator owning no vehicles rather than seeing everything.
func (h *Handler) jobScopeFor(r *http.Request) *application.JobScope {
	ownerOperatorID, limited := limitedOwnerOperatorID(r)
	if !limited {
		return nil
	}
	if h.ownedVehicles == nil {
		return application.NewOwnerOperatorScope(ownerOperatorID, nil)
	}
	tenantID := httpx.TenantIDFromRequest(r)
	ids, err := h.ownedVehicles.OwnedVehicleIDs(r.Context(), tenantID, ownerOperatorID)
	if err != nil {
		log.Printf("dispatch: owned vehicles lookup failed for owner_operator_id=%s tenant_id=%s: %v", ownerOperatorID, tenantID, err)
		return application.NewOwnerOperatorScope(ownerOperatorID, nil)
	}
	return application.NewOwnerOperatorScope(ownerOperatorID, ids)
}

// jobsVisible reports whether every job exists for the caller; one outside
// their scope does not.
func (h *Handler) jobsVisible(r *http.Request, scope *application.JobScope, jobIDs ...string) bool {
	if scope == nil {
		return true
	}
	if h.jobLifecycleUseCase == nil {
		return false
	}
	tenantID := httpx.TenantIDFromRequest(r)
	for _, jobID := range jobIDs {
		job, err := h.jobLifecycleUseCase.GetJob(r.Context(), tenantID, jobID)
		if err != nil || !scope.Allows(job) {
			return false
		}
	}
	return true
}

// allowsNamedVehicle is whether a vehicle a request names is in scope. Naming
// none keeps the job's current vehicle, which the job check already covered.
func allowsNamedVehicle(scope *application.JobScope, vehicleID string) bool {
	vehicleID = strings.TrimSpace(vehicleID)
	return vehicleID == "" || scope.AllowsVehicle(vehicleID)
}

func writeJobNotFound(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, application.ErrJobNotFound.Error())
}

// hiddenFromOwnerOperators serves routes that act on the whole tenant, which
// a limited owner operator must not see, so to them the route does not exist.
func hiddenFromOwnerOperators(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, limited := limitedOwnerOperatorID(r); limited {
			httpx.NotFound(w, r)
			return
		}
		next(w, r)
	}
}
