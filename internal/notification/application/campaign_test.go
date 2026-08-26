package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeCampaignRepository struct {
	campaigns  map[string]domain.Campaign
	recipients map[string][]domain.CampaignRecipient
}

func newFakeCampaignRepository() *fakeCampaignRepository {
	return &fakeCampaignRepository{
		campaigns:  map[string]domain.Campaign{},
		recipients: map[string][]domain.CampaignRecipient{},
	}
}

func (r *fakeCampaignRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeCampaignRepository) SaveCampaign(_ context.Context, campaign domain.Campaign) (domain.Campaign, error) {
	r.campaigns[campaign.ID] = campaign
	return campaign, nil
}

func (r *fakeCampaignRepository) FindCampaign(_ context.Context, tenantID, campaignID string) (domain.Campaign, bool, error) {
	campaign, ok := r.campaigns[campaignID]
	if !ok || campaign.TenantID != tenantID {
		return domain.Campaign{}, false, nil
	}
	return campaign, true, nil
}

func (r *fakeCampaignRepository) ListCampaigns(_ context.Context, query ListCampaignsQuery) ([]domain.Campaign, int, error) {
	out := []domain.Campaign{}
	for _, campaign := range r.campaigns {
		if campaign.TenantID != query.TenantID {
			continue
		}
		if query.Status != "" && campaign.Status != query.Status {
			continue
		}
		out = append(out, campaign)
	}
	return out, len(out), nil
}

func (r *fakeCampaignRepository) SaveRecipients(_ context.Context, recipients []domain.CampaignRecipient) error {
	for _, recipient := range recipients {
		r.recipients[recipient.CampaignID] = append(r.recipients[recipient.CampaignID], recipient)
	}
	return nil
}

func (r *fakeCampaignRepository) SaveRecipient(_ context.Context, recipient domain.CampaignRecipient) error {
	list := r.recipients[recipient.CampaignID]
	for index := range list {
		if list[index].ID == recipient.ID {
			list[index] = recipient
			r.recipients[recipient.CampaignID] = list
			return nil
		}
	}
	return errors.New("recipient not found")
}

func (r *fakeCampaignRepository) ListRecipients(_ context.Context, tenantID, campaignID string) ([]domain.CampaignRecipient, error) {
	out := []domain.CampaignRecipient{}
	for _, recipient := range r.recipients[campaignID] {
		if recipient.TenantID == tenantID {
			out = append(out, recipient)
		}
	}
	return out, nil
}

func (r *fakeCampaignRepository) FindRecipientByProviderMessageID(_ context.Context, id string) (domain.CampaignRecipient, bool, error) {
	for _, list := range r.recipients {
		for _, recipient := range list {
			if recipient.ProviderMessageID != "" && recipient.ProviderMessageID == id {
				return recipient, true, nil
			}
		}
	}
	return domain.CampaignRecipient{}, false, nil
}

func (r *fakeCampaignRepository) SummarizeAll(_ context.Context, tenantID string) (domain.CampaignProgress, error) {
	all := []domain.CampaignRecipient{}
	for _, list := range r.recipients {
		for _, recipient := range list {
			if recipient.TenantID == tenantID {
				all = append(all, recipient)
			}
		}
	}
	return domain.SummarizeRecipients(all), nil
}

type fakeSuppression struct {
	blocked map[string]bool
}

func newFakeSuppression() *fakeSuppression { return &fakeSuppression{blocked: map[string]bool{}} }

func (s *fakeSuppression) EnsureSchema(context.Context) error { return nil }

func (s *fakeSuppression) IsSuppressed(_ context.Context, tenantID, destination string) (bool, error) {
	return s.blocked[tenantID+":"+destination], nil
}

func (s *fakeSuppression) Suppress(_ context.Context, tenantID, destination, _ string) error {
	s.blocked[tenantID+":"+destination] = true
	return nil
}

func (s *fakeSuppression) Release(_ context.Context, tenantID, destination string) error {
	delete(s.blocked, tenantID+":"+destination)
	return nil
}

type campaignProvider struct {
	sent []string
	fail map[string]bool
}

func (p *campaignProvider) Send(_ context.Context, _, _, recipient, _ string) (string, error) {
	if p.fail[recipient] {
		return "", errors.New("provider rejected the number")
	}
	p.sent = append(p.sent, recipient)
	return "accepted", nil
}

type campaignTemplateProvider struct {
	sent   []string
	nextID int
}

func (p *campaignTemplateProvider) SendTemplate(_ context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error) {
	p.nextID++
	p.sent = append(p.sent, request.Recipient)
	return domain.WhatsAppSendResult{Status: "accepted", MessageID: "wamid." + string(rune('a'+p.nextID))}, nil
}

type campaignCharger struct {
	charges []UsageCharge
}

func (c *campaignCharger) Charge(_ context.Context, charge UsageCharge) error {
	c.charges = append(c.charges, charge)
	return nil
}

type campaignRefusingCharger struct{ calls int }

func (c *campaignRefusingCharger) Charge(context.Context, UsageCharge) error {
	c.calls++
	return errors.New("wallet balance does not cover this")
}

type campaignClock struct{ now time.Time }

func (c campaignClock) Now() time.Time { return c.now }

type campaignIDs struct{ n int }

func (g *campaignIDs) New(prefix string) string {
	g.n++
	return prefix + "-" + string(rune('0'+g.n))
}

type campaignKit struct {
	repo        *fakeCampaignRepository
	suppression *fakeSuppression
	provider    *campaignProvider
	templates   *campaignTemplateProvider
	charger     *campaignCharger
	create      *CreateCampaignUseCase
	send        *SendCampaignUseCase
	receipt     *ApplyCampaignReceiptUseCase
}

func newCampaignKit() *campaignKit {
	repo := newFakeCampaignRepository()
	suppression := newFakeSuppression()
	provider := &campaignProvider{fail: map[string]bool{}}
	templates := &campaignTemplateProvider{}
	charger := &campaignCharger{}
	clock := campaignClock{now: time.Unix(1_700_000_000, 0).UTC()}

	return &campaignKit{
		repo:        repo,
		suppression: suppression,
		provider:    provider,
		templates:   templates,
		charger:     charger,
		create:      NewCreateCampaignUseCase(repo, suppression, clock, &campaignIDs{}),
		send:        NewSendCampaignUseCase(repo, suppression, provider, templates, "en", charger, clock),
		receipt:     NewApplyCampaignReceiptUseCase(repo, clock),
	}
}

func smsCampaign(kit *campaignKit, t *testing.T, numbers ...string) domain.Campaign {
	t.Helper()
	recipients := make([]CampaignRecipientInput, 0, len(numbers))
	for index, number := range numbers {
		recipients = append(recipients, CampaignRecipientInput{
			DriverID:    "driver-" + string(rune('a'+index)),
			Channel:     "sms",
			Destination: number,
		})
	}

	campaign, err := kit.create.Execute(context.Background(), CreateCampaignCommand{
		TenantID:   "tenant-a",
		Name:       "Weekly update",
		Channels:   []string{"sms"},
		Bodies:     map[string]string{"sms": "Depot closes at 6"},
		Recipients: recipients,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	return campaign
}

func TestCampaignChargesOnceForTheWholeRun(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002", "+2348000000003")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	// One charge covering three messages, not three charges. Charging per
	// message lets a tenant run out halfway through with half the drivers
	// already messaged.
	if len(kit.charger.charges) != 1 {
		t.Fatalf("expected one charge, got %d", len(kit.charger.charges))
	}
	if kit.charger.charges[0].Quantity != 3 {
		t.Fatalf("expected quantity 3, got %d", kit.charger.charges[0].Quantity)
	}
}

func TestCampaignSendsNothingWhenTheWalletRefuses(t *testing.T) {
	kit := newCampaignKit()
	charger := &campaignRefusingCharger{}
	kit.send = NewSendCampaignUseCase(
		kit.repo, kit.suppression, kit.provider, kit.templates, "en", charger,
		campaignClock{now: time.Unix(1_700_000_000, 0).UTC()},
	)
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err == nil {
		t.Fatal("expected the send to be refused")
	}

	// The check happens before anything goes out, so nobody is messaged.
	if len(kit.provider.sent) != 0 {
		t.Fatalf("expected no messages sent, got %d", len(kit.provider.sent))
	}
}

func TestCampaignSkipsSomeoneWhoOptedOut(t *testing.T) {
	kit := newCampaignKit()
	_ = kit.suppression.Suppress(context.Background(), "tenant-a", "+2348000000002", "asked to stop")
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	for _, sent := range kit.provider.sent {
		if sent == "+2348000000002" {
			t.Fatal("messaged a recipient who opted out")
		}
	}
	// And they are not billed for a message nobody received.
	if kit.charger.charges[0].Quantity != 1 {
		t.Fatalf("expected to charge for one message, got %d", kit.charger.charges[0].Quantity)
	}
}

func TestCampaignHonoursAnOptOutThatArrivedAfterTheCampaignWasBuilt(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")
	// Between building the campaign and sending it, the driver opts out.
	_ = kit.suppression.Suppress(context.Background(), "tenant-a", "+2348000000001", "stop")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(kit.provider.sent) != 0 {
		t.Fatal("messaged a recipient who opted out before the send")
	}
}

func TestRetrySendsOnlyToTheFailures(t *testing.T) {
	kit := newCampaignKit()
	kit.provider.fail["+2348000000002"] = true
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	kit.provider.sent = nil
	kit.provider.fail = map[string]bool{}
	result, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID, RetryFailedOnly: true,
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	// Exactly the one that failed. Re-sending to the other would message and
	// charge twice for a message already received.
	if len(kit.provider.sent) != 1 || kit.provider.sent[0] != "+2348000000002" {
		t.Fatalf("unexpected retry sends: %v", kit.provider.sent)
	}
	if result.Attempted != 1 {
		t.Fatalf("expected one attempt, got %d", result.Attempted)
	}
}

func TestRetryDoesNotContactSomeoneWhoOptedOut(t *testing.T) {
	kit := newCampaignKit()
	_ = kit.suppression.Suppress(context.Background(), "tenant-a", "+2348000000001", "stop")
	campaign := smsCampaign(kit, t, "+2348000000001")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	kit.provider.sent = nil
	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID, RetryFailedOnly: true,
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	// Suppressed is not a failure to retry — the recipient asked not to be
	// contacted, and a retry would contact them.
	if len(kit.provider.sent) != 0 {
		t.Fatal("retry contacted a suppressed recipient")
	}
}

func TestWhatsAppCampaignKeepsTheProviderMessageID(t *testing.T) {
	kit := newCampaignKit()
	campaign, err := kit.create.Execute(context.Background(), CreateCampaignCommand{
		TenantID:     "tenant-a",
		Name:         "Weekly update",
		Channels:     []string{"whatsapp"},
		Bodies:       map[string]string{"whatsapp": "Depot closes at 6"},
		TemplateName: "weekly_update",
		Recipients: []CampaignRecipientInput{
			{DriverID: "d1", Channel: "whatsapp", Destination: "+2348000000001"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	recipients, _ := kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	// Without the id a later delivery or read receipt has nothing to match on.
	if !strings.HasPrefix(recipients[0].ProviderMessageID, "wamid.") {
		t.Fatalf("expected a provider message id, got %q", recipients[0].ProviderMessageID)
	}
}

func TestReceiptsAdvanceButNeverGoBackwards(t *testing.T) {
	kit := newCampaignKit()
	campaign, _ := kit.create.Execute(context.Background(), CreateCampaignCommand{
		TenantID: "tenant-a", Name: "n", Channels: []string{"whatsapp"},
		Bodies: map[string]string{"whatsapp": "hi"}, TemplateName: "t",
		Recipients: []CampaignRecipientInput{{DriverID: "d1", Channel: "whatsapp", Destination: "+2348000000001"}},
	})
	_, _ = kit.send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})

	recipients, _ := kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	messageID := recipients[0].ProviderMessageID

	// The read receipt overtakes the delivered one, which is normal.
	if _, err := kit.receipt.Execute(context.Background(), messageID, domain.RecipientStateRead, ""); err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if _, err := kit.receipt.Execute(context.Background(), messageID, domain.RecipientStateDelivered, ""); err != nil {
		t.Fatalf("late delivered receipt: %v", err)
	}

	recipients, _ = kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	if recipients[0].State != domain.RecipientStateRead {
		t.Fatalf("a late delivered receipt walked the recipient backwards to %s", recipients[0].State)
	}
	// A read message was delivered even if that receipt never arrived.
	if recipients[0].DeliveredAt == nil {
		t.Fatal("expected delivered to be inferred from the read receipt")
	}
}

func TestReceiptForAMessageThatIsNotOursIsIgnored(t *testing.T) {
	kit := newCampaignKit()

	matched, err := kit.receipt.Execute(context.Background(), "wamid.unknown", domain.RecipientStateDelivered, "")
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	// A one-off send reports the same way and is simply not a campaign.
	if matched {
		t.Fatal("expected an unknown message id to be ignored")
	}
}

func TestCampaignCompletesOnceEveryRecipientSettles(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")

	result, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// SMS reports no receipts, so a sent message is as far as it goes and the
	// campaign settles immediately.
	if result.Campaign.Status != domain.CampaignStatusSending {
		t.Fatalf("expected sending while sms sits at sent, got %s", result.Campaign.Status)
	}
	if result.Progress.Sent != 1 {
		t.Fatalf("expected one sent, got %+v", result.Progress)
	}
}
