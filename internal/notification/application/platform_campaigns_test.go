package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

type claimingCampaignRepo struct {
	*fakeCampaignRepository
}

func (r claimingCampaignRepo) ClaimScheduledCampaign(_ context.Context, tenantID, id string, _ time.Time) (bool, error) {
	campaign, ok := r.campaigns[id]
	if !ok || campaign.TenantID != tenantID || campaign.Status != domain.CampaignStatusScheduled {
		return false, nil
	}
	campaign.Status = domain.CampaignStatusSending
	r.campaigns[id] = campaign
	return true, nil
}

type staticTemplates struct{ templates []domain.WhatsAppTemplate }

func (s staticTemplates) CreateTemplate(context.Context, domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error) {
	return domain.WhatsAppTemplateCreateResponse{}, errors.New("not used")
}

func (s staticTemplates) ListTemplates(context.Context, domain.WhatsAppTemplateListRequest) (domain.WhatsAppTemplateListResponse, error) {
	return domain.WhatsAppTemplateListResponse{Data: s.templates}, nil
}

type platformCampaignKit struct {
	*campaignKit
	clock     *campaignClock
	campaigns *PlatformCampaigns
}

func newPlatformCampaignKit(templates ...domain.WhatsAppTemplate) *platformCampaignKit {
	kit := newCampaignKit()
	clock := &campaignClock{now: time.Unix(1_700_000_000, 0).UTC()}
	repo := claimingCampaignRepo{kit.repo}
	create := NewCreateCampaignUseCase(repo, kit.suppression, clock, &campaignIDs{n: 50})
	send := NewSendCampaignUseCase(repo, kit.suppression, kit.provider, kit.templates, "en", nil, clock)
	return &platformCampaignKit{
		campaignKit: kit,
		clock:       clock,
		campaigns:   NewPlatformCampaigns(repo, create, send, staticTemplates{templates}, clock),
	}
}

func approved(name, body string) domain.WhatsAppTemplate {
	return domain.WhatsAppTemplate{Name: name, Status: "APPROVED", Language: "en", Components: []map[string]any{{"type": "BODY", "text": body}}}
}

func smsCommand(numbers ...string) PlatformCampaignCommand {
	recipients := make([]CampaignRecipientInput, 0, len(numbers))
	for _, number := range numbers {
		recipients = append(recipients, CampaignRecipientInput{DriverID: "tenant-" + number, Channel: "sms", Destination: number})
	}
	return PlatformCampaignCommand{Name: "Maintenance window", Channels: []string{"sms"}, Bodies: map[string]string{"sms": "Planned downtime tonight."}, Recipients: recipients}
}

func TestPlatformCampaignRunsAsTheSystemTenantWithoutACharge(t *testing.T) {
	kit := newPlatformCampaignKit()
	ctx := context.Background()

	created, err := kit.campaigns.Create(ctx, smsCommand("2348000000001", "2348000000002"))
	if err != nil || created.Status != domain.CampaignStatusDraft || created.Progress.Recipients != 2 {
		t.Fatalf("create: %+v err=%v", created, err)
	}
	if stored := kit.repo.campaigns[created.ID]; stored.TenantID != identity.SystemTenantID {
		t.Fatalf("campaign tenant = %q", stored.TenantID)
	}
	result, err := kit.campaigns.Send(ctx, created.ID, false)
	if err != nil || result.Attempted != 2 || len(kit.provider.sent) != 2 {
		t.Fatalf("send: %+v sent=%v err=%v", result, kit.provider.sent, err)
	}
	if len(kit.charger.charges) != 0 {
		t.Fatalf("the platform is not charged: %v", kit.charger.charges)
	}
	if result.Campaign.Progress.Sent != 2 {
		t.Fatalf("progress: %+v", result.Campaign.Progress)
	}
}

func TestPlatformCampaignListAndDetailHideOtherTenants(t *testing.T) {
	kit := newPlatformCampaignKit()
	ctx := context.Background()
	mine, _ := kit.campaigns.Create(ctx, smsCommand("2348000000001"))
	tenantCampaign := smsCampaign(kit.campaignKit, t, "2348000000009")

	list, err := kit.campaigns.List(ctx, ListCampaignsQuery{})
	if err != nil || list.Total != 1 || list.Items[0].ID != mine.ID {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	if _, err := kit.campaigns.Get(ctx, tenantCampaign.ID); !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("a tenant's campaign must not be readable here: %v", err)
	}
	if _, err := kit.campaigns.Send(ctx, tenantCampaign.ID, false); !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("or sendable: %v", err)
	}
	detail, err := kit.campaigns.Get(ctx, mine.ID)
	if err != nil || len(detail.Recipients) != 1 || detail.Recipients[0].AudienceRef != "tenant-2348000000001" {
		t.Fatalf("detail: %+v err=%v", detail, err)
	}
}

func TestPlatformCampaignChecksTheWhatsAppTemplate(t *testing.T) {
	kit := newPlatformCampaignKit(
		approved("announce", "Maintenance tonight."),
		approved("needs_value", "Hello {{1}}"),
		domain.WhatsAppTemplate{Name: "pending_one", Status: "PENDING", Components: []map[string]any{{"type": "BODY", "text": "x"}}},
	)
	ctx := context.Background()
	whatsapp := func(template string) PlatformCampaignCommand {
		return PlatformCampaignCommand{
			Name: "Notice", Channels: []string{"whatsapp"}, Bodies: map[string]string{"whatsapp": "Maintenance tonight."}, TemplateName: template,
			Recipients: []CampaignRecipientInput{{DriverID: "t1", Channel: "whatsapp", Destination: "2348000000001"}},
		}
	}

	if _, err := kit.campaigns.Create(ctx, whatsapp("announce")); err != nil {
		t.Fatalf("a plain approved template: %v", err)
	}
	var input InboxInputError
	for _, name := range []string{"missing", "pending_one", "needs_value"} {
		if _, err := kit.campaigns.Create(ctx, whatsapp(name)); !errors.As(err, &input) {
			t.Errorf("%s must be refused as an input error, got %v", name, err)
		}
	}
}

func TestPlatformCampaignScheduleCancelAndRunDue(t *testing.T) {
	kit := newPlatformCampaignKit()
	ctx := context.Background()
	var input InboxInputError

	soon := smsCommand("2348000000001")
	soonAt := kit.clock.now.Add(10 * time.Second)
	soon.ScheduledFor = &soonAt
	if _, err := kit.campaigns.Create(ctx, soon); !errors.As(err, &input) {
		t.Fatalf("too soon: %v", err)
	}

	later := kit.clock.now.Add(time.Hour)
	first, second := smsCommand("2348000000001"), smsCommand("2348000000002")
	first.ScheduledFor, second.ScheduledFor = &later, &later
	keep, err := kit.campaigns.Create(ctx, first)
	if err != nil || keep.Status != domain.CampaignStatusScheduled {
		t.Fatalf("scheduled: %+v err=%v", keep, err)
	}
	drop, _ := kit.campaigns.Create(ctx, second)
	if cancelled, err := kit.campaigns.Cancel(ctx, drop.ID); err != nil || cancelled.Status != domain.CampaignStatusCancelled {
		t.Fatalf("cancel: %+v err=%v", cancelled, err)
	}
	if _, err := kit.campaigns.Cancel(ctx, drop.ID); !errors.As(err, &input) {
		t.Fatalf("cancel twice: %v", err)
	}
	if _, err := kit.campaigns.Send(ctx, drop.ID, false); !errors.As(err, &input) {
		t.Fatalf("a cancelled campaign cannot be sent: %v", err)
	}

	if started, _ := kit.campaigns.RunDue(ctx); started != 0 || len(kit.provider.sent) != 0 {
		t.Fatal("nothing is due yet")
	}
	kit.clock.now = kit.clock.now.Add(2 * time.Hour)
	if started, err := kit.campaigns.RunDue(ctx); err != nil || started != 1 || len(kit.provider.sent) != 1 {
		t.Fatalf("due: started=%d sent=%v err=%v", started, kit.provider.sent, err)
	}
	if started, _ := kit.campaigns.RunDue(ctx); started != 0 || len(kit.provider.sent) != 1 {
		t.Fatal("a run campaign must not run again")
	}
}
