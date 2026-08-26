package infrastructure

import (
	"context"
	"fmt"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

// JobOfferEventPublisher puts an offer on each tenant's own subject.
//
// This is the closest thing to a push notification the platform has today:
// the realtime gateway fans a tenant's subject out to whichever of that
// tenant's browsers are connected, so a carrier looking at the app sees the
// offer appear. It is not a mobile push — no APNs/FCM registration exists
// anywhere in this codebase — so a carrier who is not looking at the app right
// now is not reached by this alone. Building that is a separate piece of work.
type JobOfferEventPublisher struct {
	bus         eventbus.EventBus
	clock       SystemClock
	idGenerator TimeIDGenerator
}

func NewJobOfferEventPublisher(bus eventbus.EventBus) *JobOfferEventPublisher {
	return &JobOfferEventPublisher{bus: bus}
}

func (p *JobOfferEventPublisher) PublishOfferCreated(ctx context.Context, offer domain.JobOffer) error {
	return p.publish(ctx, offer.TargetTenantID, "job.offer.created", offer)
}

func (p *JobOfferEventPublisher) PublishOfferResponded(ctx context.Context, offer domain.JobOffer) error {
	// The offering tenant is who needs to know an offer was answered — they are
	// the one deciding whether to offer the job elsewhere next.
	return p.publish(ctx, offer.OfferingTenantID, "job.offer.responded", offer)
}

func (p *JobOfferEventPublisher) publish(ctx context.Context, tenantID, eventType string, offer domain.JobOffer) error {
	subject := fmt.Sprintf("tm.%s.%s", tenantID, eventType)
	return p.bus.Publish(ctx, subject, eventbus.EventEnvelope{
		ID:         p.idGenerator.New(),
		Type:       eventType,
		Version:    1,
		OccurredAt: p.clock.Now(),
		TenantID:   tenantID,
		Data: map[string]any{
			"offer_id":           offer.ID,
			"job_id":             offer.JobID,
			"offering_tenant_id": offer.OfferingTenantID,
			"target_tenant_id":   offer.TargetTenantID,
			"status":             offer.Status,
			"offered_rate_ngn":   offer.OfferedRateNgn,
			"expires_at":         offer.ExpiresAt,
		},
	})
}
