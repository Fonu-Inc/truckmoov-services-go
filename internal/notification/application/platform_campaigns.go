package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var ErrCampaignNotCancellable = errors.New("only a draft or scheduled campaign can be cancelled")

const dueCampaignPage = 100

// PlatformCampaignRepository adds the one atomic step scheduling needs.
type PlatformCampaignRepository interface {
	CampaignRepository
	// ClaimScheduledCampaign moves a scheduled campaign to sending and reports
	// whether this caller won it, so two instances never run the same broadcast.
	ClaimScheduledCampaign(ctx context.Context, tenantID, campaignID string, at time.Time) (bool, error)
}

type CampaignProgressView struct {
	Recipients int `json:"recipients"`
	Sent       int `json:"sent"`
	Delivered  int `json:"delivered"`
	Read       int `json:"read"`
	Failed     int `json:"failed"`
}

type CampaignView struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Channels     []string             `json:"channels"`
	Bodies       map[string]string    `json:"bodies"`
	TemplateName string               `json:"template_name"`
	Status       string               `json:"status"`
	ScheduledFor *time.Time           `json:"scheduled_for"`
	CreatedAt    time.Time            `json:"created_at"`
	Progress     CampaignProgressView `json:"progress"`
}

type CampaignRecipientView struct {
	ID            string     `json:"id"`
	AudienceRef   string     `json:"audience_ref"`
	Channel       string     `json:"channel"`
	Destination   string     `json:"destination"`
	State         string     `json:"state"`
	FailureReason string     `json:"failure_reason"`
	SentAt        *time.Time `json:"sent_at"`
	DeliveredAt   *time.Time `json:"delivered_at"`
	ReadAt        *time.Time `json:"read_at"`
}

type PlatformCampaignList struct {
	Items   []CampaignView       `json:"items"`
	Total   int                  `json:"total"`
	Summary CampaignProgressView `json:"summary"`
}

type PlatformCampaignDetail struct {
	Campaign   CampaignView            `json:"campaign"`
	Recipients []CampaignRecipientView `json:"recipients"`
}

type PlatformCampaignSendResult struct {
	Campaign  CampaignView `json:"campaign"`
	Attempted int          `json:"attempted"`
}

type PlatformCampaignCommand struct {
	Name         string
	Channels     []string
	Bodies       map[string]string
	TemplateName string
	ScheduledFor *time.Time
	Recipients   []CampaignRecipientInput
}

// PlatformCampaigns is the platform's own broadcast: the same campaign engine
// tenants use, run as the system tenant and from the platform's numbers, with
// no wallet charge because the platform is the one paying for itself.
type PlatformCampaigns struct {
	repository PlatformCampaignRepository
	create     *CreateCampaignUseCase
	send       *SendCampaignUseCase
	templates  WhatsAppTemplateProvider
	clock      Clock
}

func NewPlatformCampaigns(repository PlatformCampaignRepository, create *CreateCampaignUseCase, send *SendCampaignUseCase, templates WhatsAppTemplateProvider, clock Clock) *PlatformCampaigns {
	return &PlatformCampaigns{repository: repository, create: create, send: send, templates: templates, clock: clock}
}

func (u *PlatformCampaigns) Create(ctx context.Context, command PlatformCampaignCommand) (CampaignView, error) {
	if err := u.checkSchedule(command.ScheduledFor); err != nil {
		return CampaignView{}, err
	}
	if err := u.checkTemplate(ctx, command); err != nil {
		return CampaignView{}, err
	}
	campaign, err := u.create.Execute(ctx, CreateCampaignCommand{
		TenantID:     identity.SystemTenantID,
		Name:         command.Name,
		Channels:     command.Channels,
		Bodies:       command.Bodies,
		TemplateName: command.TemplateName,
		ScheduledFor: command.ScheduledFor,
		Recipients:   command.Recipients,
	})
	if err != nil {
		return CampaignView{}, InboxInputError(err.Error())
	}
	return u.view(ctx, campaign)
}

func (u *PlatformCampaigns) checkSchedule(at *time.Time) error {
	if at != nil && at.Before(u.clock.Now().Add(minScheduleLead)) {
		return InboxInputError("schedule the campaign at least a minute from now")
	}
	return nil
}

// A campaign sends its template with no values, so a template that asks for
// any, or for a media header or a button link, cannot be used.
func (u *PlatformCampaigns) checkTemplate(ctx context.Context, command PlatformCampaignCommand) error {
	if !hasChannel(command.Channels, "whatsapp") || strings.TrimSpace(command.TemplateName) == "" {
		return nil
	}
	template, err := findWhatsAppTemplateByName(ctx, u.templates, identity.SystemTenantID, strings.TrimSpace(command.TemplateName))
	if errors.Is(err, ErrJobInstructionTemplateNotFound) {
		return InboxInputError("that WhatsApp template does not exist")
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(template.Status), "APPROVED") {
		return InboxInputError("the WhatsApp template is not approved yet")
	}
	if _, err := buildOtherTemplateSendComponents(template, SendOtherTemplateCommand{}); err != nil {
		return InboxInputError("choose a template with no variables, media header or button link")
	}
	return nil
}

func (u *PlatformCampaigns) List(ctx context.Context, query ListCampaignsQuery) (PlatformCampaignList, error) {
	query.TenantID = identity.SystemTenantID
	campaigns, total, err := u.repository.ListCampaigns(ctx, query)
	if err != nil {
		return PlatformCampaignList{}, err
	}
	list := PlatformCampaignList{Items: make([]CampaignView, 0, len(campaigns)), Total: total}
	for _, campaign := range campaigns {
		view, err := u.view(ctx, campaign)
		if err != nil {
			return PlatformCampaignList{}, err
		}
		list.Items = append(list.Items, view)
	}
	summary, err := u.repository.SummarizeAll(ctx, identity.SystemTenantID)
	list.Summary = progressView(summary)
	return list, err
}

func (u *PlatformCampaigns) Get(ctx context.Context, id string) (PlatformCampaignDetail, error) {
	campaign, found, err := u.repository.FindCampaign(ctx, identity.SystemTenantID, strings.TrimSpace(id))
	if err != nil {
		return PlatformCampaignDetail{}, err
	}
	if !found {
		return PlatformCampaignDetail{}, ErrCampaignNotFound
	}
	recipients, err := u.repository.ListRecipients(ctx, campaign.TenantID, campaign.ID)
	if err != nil {
		return PlatformCampaignDetail{}, err
	}
	detail := PlatformCampaignDetail{Campaign: campaignView(campaign, recipients), Recipients: make([]CampaignRecipientView, 0, len(recipients))}
	for _, recipient := range recipients {
		detail.Recipients = append(detail.Recipients, recipientView(recipient))
	}
	return detail, nil
}

func (u *PlatformCampaigns) Send(ctx context.Context, id string, retryFailedOnly bool) (PlatformCampaignSendResult, error) {
	campaign, found, err := u.repository.FindCampaign(ctx, identity.SystemTenantID, strings.TrimSpace(id))
	if err != nil {
		return PlatformCampaignSendResult{}, err
	}
	if !found {
		return PlatformCampaignSendResult{}, ErrCampaignNotFound
	}
	if campaign.Status == domain.CampaignStatusCancelled {
		return PlatformCampaignSendResult{}, InboxInputError("this campaign was cancelled")
	}
	return u.run(ctx, campaign.ID, retryFailedOnly)
}

func (u *PlatformCampaigns) run(ctx context.Context, id string, retryFailedOnly bool) (PlatformCampaignSendResult, error) {
	result, err := u.send.Execute(ctx, SendCampaignCommand{TenantID: identity.SystemTenantID, CampaignID: id, RetryFailedOnly: retryFailedOnly})
	if err != nil && result.Campaign.ID == "" {
		return PlatformCampaignSendResult{}, err
	}
	view, viewErr := u.view(ctx, result.Campaign)
	return PlatformCampaignSendResult{Campaign: view, Attempted: result.Attempted}, errors.Join(err, viewErr)
}

func (u *PlatformCampaigns) Cancel(ctx context.Context, id string) (CampaignView, error) {
	campaign, found, err := u.repository.FindCampaign(ctx, identity.SystemTenantID, strings.TrimSpace(id))
	if err != nil {
		return CampaignView{}, err
	}
	if !found {
		return CampaignView{}, ErrCampaignNotFound
	}
	if campaign.Status != domain.CampaignStatusDraft && campaign.Status != domain.CampaignStatusScheduled {
		return CampaignView{}, InboxInputError(ErrCampaignNotCancellable.Error())
	}
	campaign.Status = domain.CampaignStatusCancelled
	campaign.UpdatedAt = u.clock.Now()
	saved, err := u.repository.SaveCampaign(ctx, campaign)
	if err != nil {
		return CampaignView{}, err
	}
	return u.view(ctx, saved)
}

// RunDue sends every scheduled campaign whose time has come.
func (u *PlatformCampaigns) RunDue(ctx context.Context) (int, error) {
	now := u.clock.Now()
	campaigns, _, err := u.repository.ListCampaigns(ctx, ListCampaignsQuery{TenantID: identity.SystemTenantID, Status: domain.CampaignStatusScheduled, Limit: dueCampaignPage})
	if err != nil {
		return 0, err
	}
	started := 0
	var failures error
	for _, campaign := range campaigns {
		if campaign.ScheduledFor == nil || campaign.ScheduledFor.After(now) {
			continue
		}
		won, err := u.repository.ClaimScheduledCampaign(ctx, campaign.TenantID, campaign.ID, now)
		if err != nil || !won {
			failures = errors.Join(failures, err)
			continue
		}
		started++
		_, runErr := u.run(ctx, campaign.ID, false)
		failures = errors.Join(failures, runErr)
	}
	return started, failures
}

func (u *PlatformCampaigns) view(ctx context.Context, campaign domain.Campaign) (CampaignView, error) {
	recipients, err := u.repository.ListRecipients(ctx, campaign.TenantID, campaign.ID)
	if err != nil {
		return CampaignView{}, err
	}
	return campaignView(campaign, recipients), nil
}

// campaignView settles the status from the recipients once a campaign has
// started, because receipts keep arriving after the send run has returned.
func campaignView(campaign domain.Campaign, recipients []domain.CampaignRecipient) CampaignView {
	status := campaign.Status
	if (status == domain.CampaignStatusSending || status == domain.CampaignStatusCompleted) && len(recipients) > 0 {
		status = domain.ResolveCampaignStatus(recipients)
	}
	return CampaignView{
		ID:           campaign.ID,
		Name:         campaign.Name,
		Channels:     campaign.Channels,
		Bodies:       campaign.Bodies,
		TemplateName: campaign.TemplateName,
		Status:       status,
		ScheduledFor: campaign.ScheduledFor,
		CreatedAt:    campaign.CreatedAt,
		Progress:     progressView(domain.SummarizeRecipients(recipients)),
	}
}

func progressView(progress domain.CampaignProgress) CampaignProgressView {
	return CampaignProgressView{Recipients: progress.Recipients, Sent: progress.Sent, Delivered: progress.Delivered, Read: progress.Read, Failed: progress.Failed}
}

func recipientView(recipient domain.CampaignRecipient) CampaignRecipientView {
	return CampaignRecipientView{
		ID:            recipient.ID,
		AudienceRef:   recipient.DriverID,
		Channel:       recipient.Channel,
		Destination:   recipient.Destination,
		State:         recipient.State,
		FailureReason: recipient.FailureReason,
		SentAt:        recipient.SentAt,
		DeliveredAt:   recipient.DeliveredAt,
		ReadAt:        recipient.ReadAt,
	}
}

func hasChannel(channels []string, wanted string) bool {
	for _, channel := range channels {
		if strings.EqualFold(strings.TrimSpace(channel), wanted) {
			return true
		}
	}
	return false
}
