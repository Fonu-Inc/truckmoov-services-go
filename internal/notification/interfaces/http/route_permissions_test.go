package notificationhttp

import (
	"net/http"
	"testing"

	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

// Every permission a route needs is one some predefined role can hold, so
// no route asks for something nobody could ever be given.
func TestEveryRoutePermissionIsHoldable(t *testing.T) {
	routes := httpx.NewRoutes(http.NewServeMux(), false, nil, nil)
	(&Handler{}).RegisterRoutes(routes)
	for _, permission := range routes.Permissions() {
		if !identity.Holdable(permission) {
			t.Fatalf("%s needs %s, which no role can hold", "notification", permission)
		}
	}
}
