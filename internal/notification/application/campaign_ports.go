package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type CampaignRepository interface {
	EnsureSchema(ctx context.Context) error
	SaveCampaign(ctx context.Context, campaign domain.Campaign) (domain.Campaign, error)
	// SaveCampaignIfStatus writes the campaign only while its stored status is
	// still expected, and reports whether it did. A run that finds the status
	// moved under it reloads and decides again instead of overwriting.
	SaveCampaignIfStatus(ctx context.Context, campaign domain.Campaign, expected string) (bool, error)
	FindCampaign(ctx context.Context, tenantID, campaignID string) (domain.Campaign, bool, error)
	ListCampaigns(ctx context.Context, query ListCampaignsQuery) ([]domain.Campaign, int, error)
	// ClaimCampaign atomically moves a draft, scheduled or paused campaign to sending,
	// or a sending one whose run died (nothing touched since staleBefore, work
	// still open), and reports whether this caller won it.
	ClaimCampaign(ctx context.Context, tenantID, campaignID string, at, staleBefore time.Time) (domain.Campaign, bool, error)
	// ListDueCampaigns spans tenants: scheduled campaigns whose time has come,
	// and sending campaigns a crashed run left behind. Each row carries its own
	// tenant, and every later call on it is keyed by that tenant.
	ListDueCampaigns(ctx context.Context, now, staleBefore time.Time, limit int) ([]domain.Campaign, error)

	SaveRecipients(ctx context.Context, recipients []domain.CampaignRecipient) error
	SaveRecipient(ctx context.Context, recipient domain.CampaignRecipient) error
	ListRecipients(ctx context.Context, tenantID, campaignID string) ([]domain.CampaignRecipient, error)
	// ClaimRecipient atomically moves one recipient from the given state to
	// sending, so two runs never send to the same row.
	ClaimRecipient(ctx context.Context, tenantID, recipientID, from string, at time.Time) (bool, error)
	// FailInterruptedRecipients turns rows a dead run left in sending into
	// failures: whether the message left is unknown, so they are retried only
	// on request.
	FailInterruptedRecipients(ctx context.Context, tenantID, campaignID string, staleBefore, at time.Time) error
	// ClaimInterruptedForRetry marks, for their one automatic retry, interrupted
	// recipients that no provider message id was ever reported for, once they
	// have waited since before interruptedBefore. It returns the rows it took.
	ClaimInterruptedForRetry(ctx context.Context, interruptedBefore, at time.Time, limit int) ([]domain.CampaignRecipient, error)
	// ReleaseAutoRetry gives the automatic retry back when it could not run.
	ReleaseAutoRetry(ctx context.Context, tenantID string, recipientIDs []string) error
	// Matches a provider receipt back to the recipient it belongs to. Receipts
	// arrive late and out of order, so this is the only handle they carry.
	FindRecipientByProviderMessageID(ctx context.Context, providerMessageID string) (domain.CampaignRecipient, bool, error)
	// Counts across every campaign, for the summary figures. They are
	// aggregates over all campaigns, not over the page being shown.
	SummarizeAll(ctx context.Context, tenantID string) (domain.CampaignProgress, error)
}

type ListCampaignsQuery struct {
	TenantID string
	Status   string
	Query    string
	Page     int
	Limit    int
}

// SuppressionRepository holds the addresses that must never be broadcast to.
//
// Keyed by address rather than by driver: an opt-out belongs to the phone
// number that received the message, and the same number may be reachable
// through more than one driver record.
type SuppressionRepository interface {
	EnsureSchema(ctx context.Context) error
	IsSuppressed(ctx context.Context, tenantID, destination string) (bool, error)
	Suppress(ctx context.Context, tenantID, destination, reason string) error
	Release(ctx context.Context, tenantID, destination string) error
}
