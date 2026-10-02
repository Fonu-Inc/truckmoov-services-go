package domain

import (
	"errors"
	"strings"
	"time"
)

// Offer statuses. Withdrawn is not in the reference build's vocabulary, which
// lists only PENDING/ACCEPTED/DECLINED/EXPIRED, but it is needed: when the
// same job is offered to several carriers and one accepts, the rest must close
// out. Marking them DECLINED would say the carrier chose not to take the job,
// which did not happen — nobody at that carrier did anything.
const (
	OfferStatusPending   = "pending"
	OfferStatusAccepted  = "accepted"
	OfferStatusDeclined  = "declined"
	OfferStatusExpired   = "expired"
	OfferStatusWithdrawn = "withdrawn"
)

// JobOffer is one cargo owner's offer of one job to one target organisation.
//
// The same job can be offered to several carriers at once, so a job has many
// offers and at most one of them may ever be accepted.
type JobOffer struct {
	ID    string
	JobID string
	// CarrierJobID is the job's id under the carrier once accepted. It differs
	// from JobID only when the carrier already held a job with that id.
	CarrierJobID     string
	OfferingTenantID string
	TargetTenantID   string
	// Separate from the job's own amount. The requirements note the two may
	// genuinely differ — a margin for the intermediary — and that this must be
	// intentional and visible only to the party who should see it: the carrier
	// sees the offered rate, not the cargo owner's own figure.
	OfferedRateNgn float64
	Status         string
	ExpiresAt      time.Time
	ResponseNote   string
	RespondedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func NewJobOffer(params JobOffer) (JobOffer, error) {
	if strings.TrimSpace(params.ID) == "" {
		return JobOffer{}, errors.New("offer id is required")
	}
	if strings.TrimSpace(params.JobID) == "" {
		return JobOffer{}, errors.New("job id is required")
	}
	if strings.TrimSpace(params.OfferingTenantID) == "" {
		return JobOffer{}, errors.New("offering tenant id is required")
	}
	if strings.TrimSpace(params.TargetTenantID) == "" {
		return JobOffer{}, errors.New("target tenant id is required")
	}
	if strings.TrimSpace(params.OfferingTenantID) == strings.TrimSpace(params.TargetTenantID) {
		return JobOffer{}, errors.New("an offer cannot target the tenant that made it")
	}
	if params.OfferedRateNgn <= 0 {
		return JobOffer{}, errors.New("an offered rate is required")
	}
	if params.ExpiresAt.IsZero() {
		return JobOffer{}, errors.New("an expiry is required")
	}

	status := params.Status
	if status == "" {
		status = OfferStatusPending
	}

	return JobOffer{
		ID:               strings.TrimSpace(params.ID),
		JobID:            strings.TrimSpace(params.JobID),
		CarrierJobID:     strings.TrimSpace(params.CarrierJobID),
		OfferingTenantID: strings.TrimSpace(params.OfferingTenantID),
		TargetTenantID:   strings.TrimSpace(params.TargetTenantID),
		OfferedRateNgn:   params.OfferedRateNgn,
		Status:           status,
		ExpiresAt:        params.ExpiresAt.UTC(),
		ResponseNote:     params.ResponseNote,
		RespondedAt:      params.RespondedAt,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
	}, nil
}

// CarrierJob is the job id the carrier sees after acceptance.
func (o JobOffer) CarrierJob() string {
	if o.CarrierJobID != "" {
		return o.CarrierJobID
	}
	return o.JobID
}

// IsExpired reports whether the offer's window has passed.
//
// Judged live against now, not against a stored flag: the requirements are
// explicit that accepting an expired offer must be refused even if the
// browser's copy is minutes old, and a flag set once by a sweep can itself be
// a minute stale.
func (o JobOffer) IsExpired(now time.Time) bool {
	return !o.ExpiresAt.After(now)
}

// CanRespond reports whether Accept or Decline may still be called.
func (o JobOffer) CanRespond(now time.Time) bool {
	return o.Status == OfferStatusPending && !o.IsExpired(now)
}

func (o JobOffer) Accept(note string, now time.Time) (JobOffer, error) {
	if !o.CanRespond(now) {
		return JobOffer{}, errors.New("this offer can no longer be responded to")
	}
	return o.respond(OfferStatusAccepted, note, now), nil
}

func (o JobOffer) Decline(note string, now time.Time) (JobOffer, error) {
	if !o.CanRespond(now) {
		return JobOffer{}, errors.New("this offer can no longer be responded to")
	}
	return o.respond(OfferStatusDeclined, note, now), nil
}

// Withdraw closes an offer pre-empted by another carrier's acceptance for the
// same job. Only reachable while still pending: an offer already declined or
// expired needs no further closing.
func (o JobOffer) Withdraw(now time.Time) JobOffer {
	if o.Status != OfferStatusPending {
		return o
	}
	return o.respond(OfferStatusWithdrawn, "Job accepted by another carrier", now)
}

// Expire closes a pending offer whose window passed with nobody acting.
func (o JobOffer) Expire(now time.Time) (JobOffer, error) {
	if o.Status != OfferStatusPending {
		return JobOffer{}, errors.New("only a pending offer expires")
	}
	if !o.IsExpired(now) {
		return JobOffer{}, errors.New("this offer has not expired yet")
	}
	updated := o
	updated.Status = OfferStatusExpired
	updated.UpdatedAt = now.UTC()
	return updated, nil
}

func (o JobOffer) respond(status, note string, now time.Time) JobOffer {
	updated := o
	updated.Status = status
	updated.ResponseNote = strings.TrimSpace(note)
	respondedAt := now.UTC()
	updated.RespondedAt = &respondedAt
	updated.UpdatedAt = respondedAt
	return updated
}
