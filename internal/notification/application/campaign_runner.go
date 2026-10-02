package application

import (
	"context"
	"errors"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const (
	dueCampaignPage = 100
	// interruptedRetryDelay is how long an interrupted recipient waits before
	// its one automatic retry, so a slow provider has time to confirm first.
	interruptedRetryDelay = 15 * time.Minute
)

// CampaignRunner starts every campaign that is due, whichever tenant owns it,
// charging each tenant's wallet through the normal send rules. Replicas can
// all run it: the claim inside Execute lets exactly one of them win a campaign.
type CampaignRunner struct {
	repository CampaignRepository
	send       *SendCampaignUseCase
	clock      Clock
}

func NewCampaignRunner(repository CampaignRepository, send *SendCampaignUseCase, clock Clock) *CampaignRunner {
	return &CampaignRunner{repository: repository, send: send, clock: clock}
}

// RunDue returns how many campaigns this call started.
func (r *CampaignRunner) RunDue(ctx context.Context) (int, error) {
	now := r.clock.Now()
	due, err := r.repository.ListDueCampaigns(ctx, now, now.Add(-campaignStaleAfter), dueCampaignPage)
	if err != nil {
		return 0, err
	}
	started := 0
	var failures error
	for _, campaign := range due {
		_, err := r.send.Execute(ctx, SendCampaignCommand{TenantID: campaign.TenantID, CampaignID: campaign.ID, Scheduled: true})
		if errors.Is(err, ErrCampaignAlreadyRunning) || errors.Is(err, ErrCampaignNotFound) || errors.Is(err, ErrCampaignCancelled) {
			continue
		}
		started++
		failures = errors.Join(failures, err)
	}
	return started, failures
}

// RetryInterrupted sends once more to recipients a dead run left unconfirmed,
// when no provider message id was ever reported for them. It returns how many
// campaigns it retried for. Anything that fails again stays failed, for an
// operator to retry.
func (r *CampaignRunner) RetryInterrupted(ctx context.Context) (int, error) {
	now := r.clock.Now()
	claimed, err := r.repository.ClaimInterruptedForRetry(ctx, now.Add(-interruptedRetryDelay), now, dueCampaignPage)
	if err != nil {
		return 0, err
	}
	retried := 0
	var failures error
	for _, group := range groupByCampaign(claimed) {
		_, err := r.send.Execute(ctx, SendCampaignCommand{TenantID: group.tenantID, CampaignID: group.campaignID, RetryFailedOnly: true, RecipientIDs: group.recipientIDs})
		if err != nil && givesRetryBack(err) {
			failures = errors.Join(failures, err, r.repository.ReleaseAutoRetry(ctx, group.tenantID, group.recipientIDs))
			continue
		}
		retried++
		failures = errors.Join(failures, err)
	}
	return retried, failures
}

// givesRetryBack is whether a retry that did not run leaves the recipient its
// automatic retry. A cancelled or deleted campaign and a refused wallet are
// final answers.
func givesRetryBack(err error) bool {
	return !errors.Is(err, ErrCampaignCancelled) && !errors.Is(err, ErrCampaignNotFound) && !errors.Is(err, ErrInsufficientFunds)
}

type campaignRecipientGroup struct {
	tenantID     string
	campaignID   string
	recipientIDs []string
}

func groupByCampaign(recipients []domain.CampaignRecipient) []*campaignRecipientGroup {
	groups := []*campaignRecipientGroup{}
	index := map[string]*campaignRecipientGroup{}
	for _, recipient := range recipients {
		key := recipient.TenantID + "|" + recipient.CampaignID
		group, ok := index[key]
		if !ok {
			group = &campaignRecipientGroup{tenantID: recipient.TenantID, campaignID: recipient.CampaignID}
			index[key] = group
			groups = append(groups, group)
		}
		group.recipientIDs = append(group.recipientIDs, recipient.ID)
	}
	return groups
}
