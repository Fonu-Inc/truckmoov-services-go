package application

import (
	"context"
	"errors"
)

const dueCampaignPage = 100

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
		_, err := r.send.Execute(ctx, SendCampaignCommand{TenantID: campaign.TenantID, CampaignID: campaign.ID})
		if errors.Is(err, ErrCampaignAlreadyRunning) || errors.Is(err, ErrCampaignNotFound) || errors.Is(err, ErrCampaignCancelled) {
			continue
		}
		started++
		failures = errors.Join(failures, err)
	}
	return started, failures
}
