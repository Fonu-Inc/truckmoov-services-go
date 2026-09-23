package httpx

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

// AccessRefusedSubject carries every refused request to auth, which keeps
// the per-IP record the platform's automatic IP blocking counts.
const AccessRefusedSubject = "tm.system.access.refused"

// RefusalPublisher reports refusals on the event bus without holding up the
// response.
func RefusalPublisher(bus eventbus.EventBus, service string) RefusalReporter {
	return func(refusal Refusal) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = bus.Publish(ctx, AccessRefusedSubject, eventbus.EventEnvelope{
				Type:       "access.refused",
				Version:    1,
				OccurredAt: time.Now().UTC(),
				TenantID:   identity.SystemTenantID,
				Data: map[string]any{
					"service": service,
					"method":  refusal.Method,
					"path":    refusal.Path,
					"ip":      refusal.IP,
					"caller":  refusal.Audience,
					"reason":  refusal.Reason,
				},
			})
		}()
	}
}
