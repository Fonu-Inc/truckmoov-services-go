package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var (
	ErrCampaignNotFound       = errors.New("campaign not found")
	ErrCampaignAlreadyRunning = errors.New("campaign already running")
	ErrCampaignCancelled      = errors.New("campaign was cancelled")
)

const (
	campaignStaleAfter  = 15 * time.Minute
	campaignSendWorkers = 4
)

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

// Execute broadcasts a campaign. The campaign is claimed atomically first, so a
// double click or a click that races the scheduler is refused instead of
// messaging everyone twice; each recipient is claimed again before its send.
func (u *SendCampaignUseCase) Execute(ctx context.Context, command SendCampaignCommand) (SendCampaignResult, error) {
	campaign, found, err := u.repository.FindCampaign(ctx, command.TenantID, command.CampaignID)
	if err != nil {
		return SendCampaignResult{}, err
	}
	if !found {
		return SendCampaignResult{}, ErrCampaignNotFound
	}
	if campaign.Status == domain.CampaignStatusCancelled {
		return SendCampaignResult{}, ErrCampaignCancelled
	}

	prior := campaign.Status
	from := domain.RecipientStateFailed
	claimed := !command.RetryFailedOnly
	if claimed {
		from = domain.RecipientStateQueued
		var won bool
		campaign, won, err = u.claim(ctx, campaign)
		if err != nil || !won {
			return u.unclaimed(ctx, command, err)
		}
	}

	recipients, err := u.repository.ListRecipients(ctx, command.TenantID, command.CampaignID)
	if err != nil {
		return SendCampaignResult{}, err
	}
	pending := u.selectPending(recipients, command.RetryFailedOnly)
	if len(pending) == 0 && !claimed {
		return SendCampaignResult{Campaign: campaign, Progress: domain.SummarizeRecipients(recipients)}, nil
	}

	attempted, chargeErr := u.deliverAll(ctx, campaign, pending, from)
	result, err := u.settle(ctx, campaign, prior, attempted, chargeErr)
	if err != nil {
		return SendCampaignResult{}, err
	}
	if chargeErr != nil && attempted == 0 {
		return SendCampaignResult{}, chargeErr
	}
	return result, chargeErr
}

func (u *SendCampaignUseCase) claim(ctx context.Context, campaign domain.Campaign) (domain.Campaign, bool, error) {
	now := u.clock.Now()
	staleBefore := now.Add(-campaignStaleAfter)
	claimed, won, err := u.repository.ClaimCampaign(ctx, campaign.TenantID, campaign.ID, now, staleBefore)
	if err != nil || !won {
		return domain.Campaign{}, false, err
	}
	if err := u.repository.FailInterruptedRecipients(ctx, campaign.TenantID, campaign.ID, staleBefore, now); err != nil {
		return domain.Campaign{}, false, fmt.Errorf("recover interrupted recipients: %w", err)
	}
	return claimed, true, nil
}

// unclaimed explains a lost claim. Queued work on a campaign someone else is
// running is a conflict; a campaign with nothing left to send is a no-op.
func (u *SendCampaignUseCase) unclaimed(ctx context.Context, command SendCampaignCommand, claimErr error) (SendCampaignResult, error) {
	if claimErr != nil {
		return SendCampaignResult{}, claimErr
	}
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
	if campaign.Status == domain.CampaignStatusSending && hasRecipientIn(recipients, domain.RecipientStateQueued) {
		return SendCampaignResult{}, ErrCampaignAlreadyRunning
	}
	return SendCampaignResult{Campaign: campaign, Progress: domain.SummarizeRecipients(recipients)}, nil
}

func hasRecipientIn(recipients []domain.CampaignRecipient, state string) bool {
	for _, recipient := range recipients {
		if recipient.State == state {
			return true
		}
	}
	return false
}

// settle stores where the campaign ended up. A refused wallet puts an unstarted
// campaign back to what it was, so topping up lets the next run pick it up
// instead of waiting out the crash-recovery window.
func (u *SendCampaignUseCase) settle(
	ctx context.Context,
	campaign domain.Campaign,
	prior string,
	attempted int,
	chargeErr error,
) (SendCampaignResult, error) {
	updated, err := u.repository.ListRecipients(ctx, campaign.TenantID, campaign.ID)
	if err != nil {
		return SendCampaignResult{}, err
	}
	campaign.Status = domain.ResolveCampaignStatus(updated)
	if chargeErr != nil && (prior == domain.CampaignStatusDraft || prior == domain.CampaignStatusScheduled) {
		campaign.Status = prior
	}
	campaign.UpdatedAt = u.clock.Now()
	saved, err := u.repository.SaveCampaign(ctx, campaign)
	if err != nil {
		return SendCampaignResult{}, fmt.Errorf("save campaign: %w", err)
	}
	return SendCampaignResult{Campaign: saved, Progress: domain.SummarizeRecipients(updated), Attempted: attempted}, nil
}

// deliverAll bills and sends recipients on a few workers, so only recipients
// actually attempted are charged. A refused charge stops new work and leaves
// the rest as they were for a later run.
func (u *SendCampaignUseCase) deliverAll(
	ctx context.Context,
	campaign domain.Campaign,
	pending []domain.CampaignRecipient,
	from string,
) (int, error) {
	run := &deliveryRun{}
	jobs := make(chan domain.CampaignRecipient)
	var workers sync.WaitGroup
	for range campaignSendWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for recipient := range jobs {
				if run.stopped() {
					continue
				}
				run.record(u.deliverOne(ctx, campaign, recipient, from))
			}
		}()
	}
	for _, recipient := range pending {
		jobs <- recipient
	}
	close(jobs)
	workers.Wait()
	return run.result()
}

type deliveryRun struct {
	mu        sync.Mutex
	attempted int
	err       error
}

func (r *deliveryRun) stopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err != nil
}

func (r *deliveryRun) record(attempted bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil && r.err == nil {
		r.err = err
	}
	if attempted {
		r.attempted++
	}
}

func (r *deliveryRun) result() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempted, r.err
}

// deliverOne claims the row, sends, and saves. A row another run already holds
// is skipped. A failure before the send puts the row back so it is not lost.
func (u *SendCampaignUseCase) deliverOne(
	ctx context.Context,
	campaign domain.Campaign,
	recipient domain.CampaignRecipient,
	from string,
) (bool, error) {
	won, err := u.repository.ClaimRecipient(ctx, campaign.TenantID, recipient.ID, from, u.clock.Now())
	if err != nil {
		return false, fmt.Errorf("claim recipient: %w", err)
	}
	if !won {
		return false, nil
	}
	if err := u.deliver(ctx, campaign, &recipient); err != nil {
		recipient.State = from
		return false, errors.Join(err, u.repository.SaveRecipient(ctx, recipient))
	}
	if err := u.repository.SaveRecipient(ctx, recipient); err != nil {
		return false, fmt.Errorf("save recipient: %w", err)
	}
	return true, nil
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

// chargeRecipient debits one billable recipient just before their send. The
// reference is stable per recipient, so a retry or replay never charges twice.
func (u *SendCampaignUseCase) chargeRecipient(
	ctx context.Context,
	campaign domain.Campaign,
	recipient domain.CampaignRecipient,
) error {
	if u.usageCharger == nil || !IsBillable(recipient.Channel) || campaign.TenantID == identity.SystemTenantID {
		return nil
	}
	err := u.usageCharger.Charge(ctx, UsageCharge{
		TenantID:  campaign.TenantID,
		Channel:   recipient.Channel,
		Quantity:  1,
		Reason:    fmt.Sprintf("campaign %s over %s", campaign.Name, recipient.Channel),
		Reference: campaignChargeReference(campaign.ID, recipient),
	})
	if err != nil {
		return fmt.Errorf("charge campaign recipient: %w", err)
	}
	return nil
}

func campaignChargeReference(campaignID string, recipient domain.CampaignRecipient) string {
	return fmt.Sprintf("campaign:%s:%s:%s", campaignID, recipient.Channel, recipient.ID)
}

func (u *SendCampaignUseCase) deliver(
	ctx context.Context,
	campaign domain.Campaign,
	recipient *domain.CampaignRecipient,
) error {
	now := u.clock.Now()
	recipient.UpdatedAt = now

	// Re-checked at the moment of sending: an opt-out may have arrived between
	// the campaign being built and it being sent.
	suppressed, err := u.suppression.IsSuppressed(ctx, campaign.TenantID, recipient.Destination)
	if err != nil {
		return fmt.Errorf("check recipient opt-out: %w", err)
	}
	if suppressed {
		recipient.State = domain.RecipientStateSuppressed
		recipient.FailureReason = "recipient opted out"
		return nil
	}

	if err := u.chargeRecipient(ctx, campaign, *recipient); err != nil {
		return err
	}

	providerMessageID, sendErr := u.send(ctx, campaign, *recipient)
	if sendErr != nil {
		recipient.State = domain.RecipientStateFailed
		recipient.FailureReason = sendErr.Error()
		return nil
	}

	recipient.State = domain.RecipientStateSent
	recipient.FailureReason = ""
	// Empty on SMS and email: the generic provider reports a status, not an id,
	// so those channels cannot be matched to a later receipt. They settle at
	// sent rather than progressing to delivered.
	recipient.ProviderMessageID = providerMessageID
	recipient.SentAt = &now
	return nil
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
