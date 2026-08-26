package application

import (
	"context"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type CampaignRepository interface {
	EnsureSchema(ctx context.Context) error
	SaveCampaign(ctx context.Context, campaign domain.Campaign) (domain.Campaign, error)
	FindCampaign(ctx context.Context, tenantID, campaignID string) (domain.Campaign, bool, error)
	ListCampaigns(ctx context.Context, query ListCampaignsQuery) ([]domain.Campaign, int, error)

	SaveRecipients(ctx context.Context, recipients []domain.CampaignRecipient) error
	SaveRecipient(ctx context.Context, recipient domain.CampaignRecipient) error
	ListRecipients(ctx context.Context, tenantID, campaignID string) ([]domain.CampaignRecipient, error)
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
