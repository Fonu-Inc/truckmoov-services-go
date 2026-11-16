package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type JobOfferRepository interface {
	EnsureSchema(ctx context.Context) error
	Create(ctx context.Context, offer domain.JobOffer) (domain.JobOffer, error)
	FindByID(ctx context.Context, offerID string) (domain.JobOffer, bool, error)
	// List returns offers visible to one tenant — either side of the offer,
	// selected by role. Neither side may see an offer made to someone else.
	List(ctx context.Context, query JobOfferQuery) ([]domain.JobOffer, int, error)
	CountPending(ctx context.Context, targetTenantID string) (int, error)
	HasPending(ctx context.Context, offeringTenantID, jobID, targetTenantID string) (bool, error)
	// ListAccepted returns every accepted offer between one specific pair of
	// tenants, regardless of when — used by the aggregator screen to find a
	// linked operator's sub-dispatched jobs and by settlement close to total
	// a week's earnings for that pair.
	ListAccepted(ctx context.Context, offeringTenantID, targetTenantID string) ([]domain.JobOffer, error)
	// FindExpiring returns pending offers whose window has passed, for the
	// sweep that moves them to Past without anyone acting.
	FindExpiring(ctx context.Context, before time.Time, limit int) ([]domain.JobOffer, error)
	SaveExpired(ctx context.Context, offer domain.JobOffer) error
	// Decline and Withdraw are simple status changes with no cross-table work,
	// so they save independently of Accept.
	Save(ctx context.Context, offer domain.JobOffer) error

	// Accept is the one operation that touches two aggregates and must not run
	// as two separate writes: the offer becomes accepted, every sibling
	// pending offer for the same job is withdrawn, and the job itself is
	// handed to the carrier tenant. A crash between any two of those would
	// leave an offer marked accepted with the job still owned by its old
	// tenant — exactly the "accepted but unassigned" case the requirements
	// name. It runs inside one database transaction.
	Accept(ctx context.Context, offerID, targetTenantID, note string, now time.Time) (AcceptedOffer, error)
}

type AcceptedOffer struct {
	Offer domain.JobOffer
	Job   domain.Job
}

type JobOfferQuery struct {
	TenantID string
	// Role is "offering" or "target" — which side of the offer this tenant is.
	Role   string
	Status string
	Page   int
	Limit  int
}

// JobOfferEventPublisher pushes an offer to whoever needs to know it exists or
// has been answered. Offers are time-critical, and this is the closest thing
// to a push notification the platform has: an event on the tenant's own
// subject, which the realtime gateway fans out to a connected client
// immediately. It is not a mobile push notification — no APNs/FCM
// registration exists anywhere in this codebase — so a user who is not
// looking at the app right now will not be reached by this alone.
type JobOfferEventPublisher interface {
	PublishOfferCreated(ctx context.Context, offer domain.JobOffer) error
	PublishOfferResponded(ctx context.Context, offer domain.JobOffer) error
}

// ActiveJobEntitlementsReader answers how many active jobs a tenant may hold
// at once.
//
// Owned by auth, read from here rather than duplicated: the requirements name
// this and the owner-operator vehicle cap as the two limits that must come
// from one source, on pain of a tenant seeing two different numbers for the
// same thing.
type ActiveJobEntitlementsReader interface {
	// MaxActiveJobs returns nil when the tenant has no cap — unlimited, not
	// zero, matching the same "unset means unlimited" rule the platform uses
	// for every other quota.
	MaxActiveJobs(ctx context.Context, tenantID string) (*int, error)
}
