package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

var ErrCampaignNotFound = errors.New("campaign not found")

type CampaignRecipientInput struct {
	DriverID    string
	Channel     string
	Destination string
}

type CreateCampaignCommand struct {
	TenantID     string
	Name         string
	Channels     []string
	Bodies       map[string]string
	TemplateName string
	ScheduledFor *time.Time
	Recipients   []CampaignRecipientInput
}

type CreateCampaignUseCase struct {
	repository  CampaignRepository
	suppression SuppressionRepository
	clock       Clock
	idGenerator IDGenerator
}

func NewCreateCampaignUseCase(
	repository CampaignRepository,
	suppression SuppressionRepository,
	clock Clock,
	idGenerator IDGenerator,
) *CreateCampaignUseCase {
	return &CreateCampaignUseCase{repository: repository, suppression: suppression, clock: clock, idGenerator: idGenerator}
}

func (u *CreateCampaignUseCase) Execute(ctx context.Context, command CreateCampaignCommand) (domain.Campaign, error) {
	if len(command.Recipients) == 0 {
		return domain.Campaign{}, errors.New("a campaign needs at least one recipient")
	}

	now := u.clock.Now()
	status := domain.CampaignStatusDraft
	if command.ScheduledFor != nil {
		status = domain.CampaignStatusScheduled
	}

	campaign, err := domain.NewCampaign(domain.Campaign{
		ID:           u.idGenerator.New("cmp"),
		TenantID:     command.TenantID,
		Name:         command.Name,
		Channels:     command.Channels,
		Bodies:       command.Bodies,
		TemplateName: command.TemplateName,
		Status:       status,
		ScheduledFor: command.ScheduledFor,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		return domain.Campaign{}, err
	}

	saved, err := u.repository.SaveCampaign(ctx, campaign)
	if err != nil {
		return domain.Campaign{}, fmt.Errorf("save campaign: %w", err)
	}

	recipients := make([]domain.CampaignRecipient, 0, len(command.Recipients))
	for _, input := range command.Recipients {
		channel := strings.ToLower(strings.TrimSpace(input.Channel))
		destination := strings.TrimSpace(input.Destination)
		if destination == "" || !domain.IsCampaignChannel(channel) {
			continue
		}

		state := domain.RecipientStateQueued
		failureReason := ""
		// Checked when the list is built rather than at send time, so the
		// recipient count the operator confirms already reflects who will
		// actually be contacted.
		suppressed, err := u.suppression.IsSuppressed(ctx, command.TenantID, destination)
		if err != nil {
			return domain.Campaign{}, fmt.Errorf("check suppression: %w", err)
		}
		if suppressed {
			state = domain.RecipientStateSuppressed
			failureReason = "recipient opted out"
		}

		recipients = append(recipients, domain.CampaignRecipient{
			ID:            u.idGenerator.New("cmr"),
			CampaignID:    saved.ID,
			TenantID:      command.TenantID,
			DriverID:      strings.TrimSpace(input.DriverID),
			Channel:       channel,
			Destination:   destination,
			State:         state,
			FailureReason: failureReason,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}

	if len(recipients) == 0 {
		return domain.Campaign{}, errors.New("no reachable recipients on the selected channels")
	}
	if err := u.repository.SaveRecipients(ctx, recipients); err != nil {
		return domain.Campaign{}, fmt.Errorf("save campaign recipients: %w", err)
	}

	return saved, nil
}

type SendCampaignCommand struct {
	TenantID   string
	CampaignID string
	// RetryFailedOnly resends to the recipients that failed and nobody else.
	RetryFailedOnly bool
}

type SendCampaignResult struct {
	Campaign  domain.Campaign
	Progress  domain.CampaignProgress
	Attempted int
}

type SendCampaignUseCase struct {
	repository  CampaignRepository
	suppression SuppressionRepository
	provider    MessageProvider
	// WhatsApp goes out as a template, which is the only send path that returns
	// a provider message id. Without that id a later delivery or read receipt
	// has nothing to match against.
	templateProvider WhatsAppTemplateMessageProvider
	templateLanguage string
	usageCharger     UsageCharger
	clock            Clock
}

func NewSendCampaignUseCase(
	repository CampaignRepository,
	suppression SuppressionRepository,
	provider MessageProvider,
	templateProvider WhatsAppTemplateMessageProvider,
	templateLanguage string,
	usageCharger UsageCharger,
	clock Clock,
) *SendCampaignUseCase {
	language := strings.TrimSpace(templateLanguage)
	if language == "" {
		language = "en"
	}
	return &SendCampaignUseCase{
		repository:       repository,
		suppression:      suppression,
		provider:         provider,
		templateProvider: templateProvider,
		templateLanguage: language,
		usageCharger:     usageCharger,
		clock:            clock,
	}
}

// Execute broadcasts a campaign.
//
// The whole run is charged once, before anything is sent. A campaign to two
// hundred drivers over two channels is four hundred billable events, and
// charging per message would let a tenant discover halfway through the run that
// they could not afford it — with half the drivers already messaged.
func (u *SendCampaignUseCase) Execute(ctx context.Context, command SendCampaignCommand) (SendCampaignResult, error) {
	campaign, found, err := u.repository.FindCampaign(ctx, command.TenantID, command.CampaignID)
	if err != nil {
		return SendCampaignResult{}, err
	}
	if !found {
		return SendCampaignResult{}, ErrCampaignNotFound
	}

	recipients, err := u.repository.ListRecipients(ctx, command.TenantID, command.CampaignID)
	if err != nil {
		return SendCampaignResult{}, err
	}

	pending := u.selectPending(recipients, command.RetryFailedOnly)
	if len(pending) == 0 {
		return SendCampaignResult{
			Campaign:  campaign,
			Progress:  domain.SummarizeRecipients(recipients),
			Attempted: 0,
		}, nil
	}

	if err := u.chargeForRun(ctx, campaign, pending); err != nil {
		return SendCampaignResult{}, err
	}

	for index := range pending {
		u.deliver(ctx, campaign, &pending[index])
		if err := u.repository.SaveRecipient(ctx, pending[index]); err != nil {
			return SendCampaignResult{}, fmt.Errorf("save recipient: %w", err)
		}
	}

	updated, err := u.repository.ListRecipients(ctx, command.TenantID, command.CampaignID)
	if err != nil {
		return SendCampaignResult{}, err
	}

	campaign.Status = domain.ResolveCampaignStatus(updated)
	campaign.UpdatedAt = u.clock.Now()
	saved, err := u.repository.SaveCampaign(ctx, campaign)
	if err != nil {
		return SendCampaignResult{}, fmt.Errorf("save campaign: %w", err)
	}

	return SendCampaignResult{
		Campaign:  saved,
		Progress:  domain.SummarizeRecipients(updated),
		Attempted: len(pending),
	}, nil
}

func (u *SendCampaignUseCase) selectPending(
	recipients []domain.CampaignRecipient,
	retryFailedOnly bool,
) []domain.CampaignRecipient {
	pending := make([]domain.CampaignRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		if retryFailedOnly {
			// Suppressed is not a failure to retry: the recipient asked not to
			// be contacted, and retrying would contact them.
			if recipient.CanRetry() {
				pending = append(pending, recipient)
			}
			continue
		}
		if recipient.State == domain.RecipientStateQueued {
			pending = append(pending, recipient)
		}
	}
	return pending
}

// chargeForRun debits once for everything about to be sent.
func (u *SendCampaignUseCase) chargeForRun(
	ctx context.Context,
	campaign domain.Campaign,
	pending []domain.CampaignRecipient,
) error {
	if u.usageCharger == nil {
		return nil
	}

	perChannel := map[string]int{}
	for _, recipient := range pending {
		if IsBillable(recipient.Channel) {
			perChannel[recipient.Channel]++
		}
	}

	for channel, quantity := range perChannel {
		err := u.usageCharger.Charge(ctx, UsageCharge{
			TenantID: campaign.TenantID,
			Channel:  channel,
			Quantity: quantity,
			Reason:   fmt.Sprintf("campaign %s over %s", campaign.Name, channel),
			// The campaign and channel together, so replaying the same run does
			// not charge twice.
			Reference: fmt.Sprintf("campaign:%s:%s", campaign.ID, channel),
		})
		if err != nil {
			return fmt.Errorf("charge campaign run: %w", err)
		}
	}
	return nil
}

func (u *SendCampaignUseCase) deliver(
	ctx context.Context,
	campaign domain.Campaign,
	recipient *domain.CampaignRecipient,
) {
	now := u.clock.Now()
	recipient.UpdatedAt = now

	// Re-checked at the moment of sending: an opt-out may have arrived between
	// the campaign being built and it being sent.
	suppressed, err := u.suppression.IsSuppressed(ctx, campaign.TenantID, recipient.Destination)
	if err == nil && suppressed {
		recipient.State = domain.RecipientStateSuppressed
		recipient.FailureReason = "recipient opted out"
		return
	}

	providerMessageID, sendErr := u.send(ctx, campaign, *recipient)
	if sendErr != nil {
		recipient.State = domain.RecipientStateFailed
		recipient.FailureReason = sendErr.Error()
		return
	}

	recipient.State = domain.RecipientStateSent
	recipient.FailureReason = ""
	// Empty on SMS and email: the generic provider reports a status, not an id,
	// so those channels cannot be matched to a later receipt. They settle at
	// sent rather than progressing to delivered.
	recipient.ProviderMessageID = providerMessageID
	recipient.SentAt = &now
}

func (u *SendCampaignUseCase) send(
	ctx context.Context,
	campaign domain.Campaign,
	recipient domain.CampaignRecipient,
) (string, error) {
	if recipient.Channel == "whatsapp" {
		if u.templateProvider == nil {
			return "", errors.New("no whatsapp template provider is configured")
		}
		result, err := u.templateProvider.SendTemplate(ctx, domain.WhatsAppTemplateMessageRequest{
			TenantID:  campaign.TenantID,
			Recipient: recipient.Destination,
			Name:      campaign.TemplateName,
			Language:  u.templateLanguage,
		})
		if err != nil {
			return "", err
		}
		return result.MessageID, nil
	}

	body := campaign.Bodies[recipient.Channel]
	if _, err := u.provider.Send(ctx, campaign.TenantID, recipient.Channel, recipient.Destination, body); err != nil {
		return "", err
	}
	return "", nil
}
