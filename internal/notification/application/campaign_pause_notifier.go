package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// EmailCampaignPauseNotifier emails the tenant's own contact when a scheduled
// campaign is paused because its wallet keeps being refused.
type EmailCampaignPauseNotifier struct {
	send     *SendMessageUseCase
	contacts TenantContactReader
	log      *slog.Logger
}

func NewEmailCampaignPauseNotifier(send *SendMessageUseCase, contacts TenantContactReader, log *slog.Logger) *EmailCampaignPauseNotifier {
	return &EmailCampaignPauseNotifier{send: send, contacts: contacts, log: log}
}

// CampaignPaused is best effort: the campaign is already paused, so a missing
// contact or a failed email is logged and the status still tells the tenant.
func (n *EmailCampaignPauseNotifier) CampaignPaused(ctx context.Context, campaign domain.Campaign) {
	contact, found, err := n.contacts.ContactFor(ctx, campaign.TenantID)
	email := strings.TrimSpace(contact.Email)
	if err != nil || !found || email == "" {
		n.log.Warn("campaign_pause_notice_skipped", "tenant", campaign.TenantID, "campaign", campaign.ID, "found", found, "error", err)
		return
	}
	body := fmt.Sprintf("Your campaign \"%s\" was paused because your wallet could not cover it on several scheduled runs. Top up your wallet, then open the campaign and press Send to resume it.", campaign.Name)
	_, err = n.send.Execute(ctx, SendMessageCommand{
		TenantID:         campaign.TenantID,
		Channel:          "email",
		Recipient:        email,
		Content:          "Your campaign was paused\n\n" + body,
		NotificationType: domain.NotificationTypeCampaign,
	})
	if err != nil {
		n.log.Warn("campaign_pause_notice_failed", "tenant", campaign.TenantID, "campaign", campaign.ID, "error", err)
	}
}
