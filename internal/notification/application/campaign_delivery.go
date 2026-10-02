package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// ApplyCampaignReceiptUseCase moves a campaign recipient forward when the
// provider reports on it.
//
// Receipts arrive late and out of order — a read can land before the delivered
// that preceded it. The state only ever advances, so an out-of-order receipt
// cannot walk a recipient backwards.
type ApplyCampaignReceiptUseCase struct {
	repository CampaignRepository
	clock      Clock
}

func NewApplyCampaignReceiptUseCase(repository CampaignRepository, clock Clock) *ApplyCampaignReceiptUseCase {
	return &ApplyCampaignReceiptUseCase{repository: repository, clock: clock}
}

// receiptRank orders the states so a later receipt cannot undo an earlier one.
func receiptRank(state string) int {
	switch state {
	case domain.RecipientStateQueued:
		return 0
	case domain.RecipientStateSent:
		return 1
	case domain.RecipientStateDelivered:
		return 2
	case domain.RecipientStateRead:
		return 3
	case domain.RecipientStateFailed, domain.RecipientStateSuppressed:
		return 4
	default:
		return 0
	}
}

func (u *ApplyCampaignReceiptUseCase) Execute(
	ctx context.Context,
	providerMessageID string,
	state string,
	failureReason string,
) (bool, error) {
	trimmed := strings.TrimSpace(providerMessageID)
	if trimmed == "" {
		return false, nil
	}

	recipient, found, err := u.repository.FindRecipientByProviderMessageID(ctx, trimmed)
	if err != nil {
		return false, err
	}
	if !found {
		// Not every message belongs to a campaign. A one-off send reports the
		// same way and is simply not ours.
		return false, nil
	}

	// A failure always applies; anything else must be a step forward.
	if state != domain.RecipientStateFailed && receiptRank(state) <= receiptRank(recipient.State) {
		return false, nil
	}

	now := u.clock.Now()
	recipient.State = state
	recipient.UpdatedAt = now
	switch state {
	case domain.RecipientStateDelivered:
		recipient.DeliveredAt = &now
	case domain.RecipientStateRead:
		recipient.ReadAt = &now
		// A read message was delivered, even if that receipt never arrived.
		if recipient.DeliveredAt == nil {
			recipient.DeliveredAt = &now
		}
	case domain.RecipientStateFailed:
		recipient.FailureReason = failureReason
	}

	if err := u.repository.SaveRecipient(ctx, recipient); err != nil {
		return false, fmt.Errorf("save campaign recipient: %w", err)
	}

	// The campaign's own status follows its recipients, so it settles as the
	// last outstanding receipt lands. A decision a person or a run made (paused,
	// cancelled, back to draft) is left alone, and the write only lands while
	// the status is the one read, so a receipt cannot overwrite a live run.
	campaign, ok, err := u.repository.FindCampaign(ctx, recipient.TenantID, recipient.CampaignID)
	if err != nil || !ok || !settlesFromRecipients(campaign.Status) {
		return true, err
	}
	recipients, err := u.repository.ListRecipients(ctx, recipient.TenantID, recipient.CampaignID)
	if err != nil {
		return true, err
	}
	expected := campaign.Status
	campaign.Status = domain.ResolveCampaignStatus(recipients)
	campaign.UpdatedAt = now
	if _, err := u.repository.SaveCampaignIfStatus(ctx, campaign, expected); err != nil {
		return true, fmt.Errorf("save campaign: %w", err)
	}

	return true, nil
}

// SuppressRecipientUseCase records an opt-out.
//
// Broadcasting to someone who asked not to be contacted is a regulatory
// problem rather than a product one, so the opt-out is honoured across every
// campaign rather than the one it came from.
type SuppressRecipientUseCase struct {
	suppression SuppressionRepository
}

func NewSuppressRecipientUseCase(suppression SuppressionRepository) *SuppressRecipientUseCase {
	return &SuppressRecipientUseCase{suppression: suppression}
}

func (u *SuppressRecipientUseCase) Execute(ctx context.Context, tenantID, destination, reason string) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("a destination is required")
	}
	return u.suppression.Suppress(ctx, tenantID, destination, reason)
}

func (u *SuppressRecipientUseCase) Release(ctx context.Context, tenantID, destination string) error {
	return u.suppression.Release(ctx, tenantID, destination)
}

// ListCampaignsUseCase returns the campaign list with its summary figures.
type ListCampaignsUseCase struct {
	repository CampaignRepository
}

func NewListCampaignsUseCase(repository CampaignRepository) *ListCampaignsUseCase {
	return &ListCampaignsUseCase{repository: repository}
}

type CampaignListItem struct {
	Campaign domain.Campaign         `json:"campaign"`
	Progress domain.CampaignProgress `json:"progress"`
}

type CampaignListResult struct {
	Items   []CampaignListItem      `json:"items"`
	Total   int                     `json:"total"`
	Summary domain.CampaignProgress `json:"summary"`
}

func (u *ListCampaignsUseCase) Execute(ctx context.Context, query ListCampaignsQuery) (CampaignListResult, error) {
	campaigns, total, err := u.repository.ListCampaigns(ctx, query)
	if err != nil {
		return CampaignListResult{}, err
	}

	items := make([]CampaignListItem, 0, len(campaigns))
	for _, campaign := range campaigns {
		recipients, err := u.repository.ListRecipients(ctx, campaign.TenantID, campaign.ID)
		if err != nil {
			return CampaignListResult{}, err
		}
		items = append(items, CampaignListItem{
			Campaign: campaign,
			Progress: domain.SummarizeRecipients(recipients),
		})
	}

	// Aggregated over every campaign, not the page. The four figures describe
	// the tenant's messaging, not whatever is currently on screen.
	summary, err := u.repository.SummarizeAll(ctx, query.TenantID)
	if err != nil {
		return CampaignListResult{}, err
	}

	return CampaignListResult{Items: items, Total: total, Summary: summary}, nil
}
