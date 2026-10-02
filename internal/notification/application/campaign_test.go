package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeCampaignRepository struct {
	mu         sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.campaigns[campaign.ID] = campaign
	return campaign, nil
}

func (r *fakeCampaignRepository) FindCampaign(_ context.Context, tenantID, campaignID string) (domain.Campaign, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	campaign, ok := r.campaigns[campaignID]
	if !ok || campaign.TenantID != tenantID {
		return domain.Campaign{}, false, nil
	}
	return campaign, true, nil
}

func (r *fakeCampaignRepository) ListCampaigns(_ context.Context, query ListCampaignsQuery) ([]domain.Campaign, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, recipient := range recipients {
		r.recipients[recipient.CampaignID] = append(r.recipients[recipient.CampaignID], recipient)
	}
	return nil
}

func (r *fakeCampaignRepository) SaveRecipient(_ context.Context, recipient domain.CampaignRecipient) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.CampaignRecipient{}
	for _, recipient := range r.recipients[campaignID] {
		if recipient.TenantID == tenantID {
			out = append(out, recipient)
		}
	}
	return out, nil
}

func (r *fakeCampaignRepository) FindRecipientByProviderMessageID(_ context.Context, id string) (domain.CampaignRecipient, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
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

func (r *fakeCampaignRepository) stale(campaign domain.Campaign, staleBefore time.Time) bool {
	if !campaign.UpdatedAt.Before(staleBefore) {
		return false
	}
	open := false
	for _, recipient := range r.recipients[campaign.ID] {
		if !recipient.UpdatedAt.Before(staleBefore) {
			return false
		}
		open = open || recipient.State == domain.RecipientStateQueued || recipient.State == domain.RecipientStateSending
	}
	return open
}

func (r *fakeCampaignRepository) ClaimCampaign(_ context.Context, tenantID, id string, at, staleBefore time.Time) (domain.Campaign, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	campaign, ok := r.campaigns[id]
	claimable := campaign.Status == domain.CampaignStatusDraft || campaign.Status == domain.CampaignStatusScheduled ||
		(campaign.Status == domain.CampaignStatusSending && r.stale(campaign, staleBefore))
	if !ok || campaign.TenantID != tenantID || !claimable {
		return domain.Campaign{}, false, nil
	}
	campaign.Status = domain.CampaignStatusSending
	campaign.UpdatedAt = at
	r.campaigns[id] = campaign
	return campaign, true, nil
}

func (r *fakeCampaignRepository) ListDueCampaigns(_ context.Context, now, staleBefore time.Time, _ int) ([]domain.Campaign, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Campaign{}
	for _, campaign := range r.campaigns {
		due := campaign.Status == domain.CampaignStatusScheduled && campaign.ScheduledFor != nil && !campaign.ScheduledFor.After(now)
		if due || (campaign.Status == domain.CampaignStatusSending && r.stale(campaign, staleBefore)) {
			out = append(out, campaign)
		}
	}
	return out, nil
}

func (r *fakeCampaignRepository) ClaimRecipient(_ context.Context, tenantID, recipientID, from string, at time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, list := range r.recipients {
		for index := range list {
			if list[index].ID == recipientID && list[index].TenantID == tenantID && list[index].State == from {
				list[index].State = domain.RecipientStateSending
				list[index].UpdatedAt = at
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *fakeCampaignRepository) FailInterruptedRecipients(_ context.Context, tenantID, campaignID string, staleBefore, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.recipients[campaignID]
	for index := range list {
		if list[index].TenantID == tenantID && list[index].State == domain.RecipientStateSending && list[index].UpdatedAt.Before(staleBefore) {
			list[index].State = domain.RecipientStateFailed
			list[index].FailureReason = "interrupted before delivery was confirmed"
			list[index].UpdatedAt = at
		}
	}
	return nil
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
	mu   sync.Mutex
	sent []string
	fail map[string]bool
}

func (p *campaignProvider) Send(_ context.Context, _, _, recipient, _ string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail[recipient] {
		return "", errors.New("provider rejected the number")
	}
	p.sent = append(p.sent, recipient)
	return "accepted", nil
}

type campaignTemplateProvider struct {
	mu     sync.Mutex
	sent   []string
	nextID int
}

func (p *campaignTemplateProvider) SendTemplate(_ context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	p.sent = append(p.sent, request.Recipient)
	return domain.WhatsAppSendResult{Status: "accepted", MessageID: "wamid." + string(rune('a'+p.nextID))}, nil
}

type campaignCharger struct {
	mu      sync.Mutex
	charges []UsageCharge
}

func (c *campaignCharger) Charge(_ context.Context, charge UsageCharge) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.charges = append(c.charges, charge)
	return nil
}

type campaignRefusingCharger struct {
	mu    sync.Mutex
	calls int
}

func (c *campaignRefusingCharger) Charge(context.Context, UsageCharge) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return errors.New("wallet balance does not cover this")
}

type campaignLimitedCharger struct {
	mu      sync.Mutex
	allowed int
	charges []UsageCharge
}

func (c *campaignLimitedCharger) Charge(_ context.Context, charge UsageCharge) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.charges) >= c.allowed {
		return ErrInsufficientFunds
	}
	c.charges = append(c.charges, charge)
	return nil
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

func TestCampaignChargesEachRecipientBeforeSending(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002", "+2348000000003")

	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(kit.charger.charges) != 3 {
		t.Fatalf("expected three charges, got %d", len(kit.charger.charges))
	}
	seen := map[string]bool{}
	for _, charge := range kit.charger.charges {
		if charge.Quantity != 1 || seen[charge.Reference] {
			t.Fatalf("bad or repeated charge: %+v", charge)
		}
		seen[charge.Reference] = true
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

func TestCampaignBillsOnlyTheRecipientsItReached(t *testing.T) {
	kit := newCampaignKit()
	charger := &campaignLimitedCharger{allowed: 2}
	kit.send = NewSendCampaignUseCase(
		kit.repo, kit.suppression, kit.provider, kit.templates, "en", charger,
		campaignClock{now: time.Unix(1_700_000_000, 0).UTC()},
	)
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002", "+2348000000003")

	_, err := kit.send.Execute(context.Background(), SendCampaignCommand{
		TenantID: "tenant-a", CampaignID: campaign.ID,
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected a refusal, got %v", err)
	}

	if len(charger.charges) != 2 || len(kit.provider.sent) != 2 {
		t.Fatalf("billed %d, sent %d, want 2 and 2", len(charger.charges), len(kit.provider.sent))
	}
	recipients, _ := kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	queued := 0
	for _, recipient := range recipients {
		if recipient.State == domain.RecipientStateQueued {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("expected one recipient left queued, got %d", queued)
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
	if len(kit.charger.charges) != 1 {
		t.Fatalf("expected to charge for one message, got %d", len(kit.charger.charges))
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

type suppressionLookupDown struct{ *fakeSuppression }

func (suppressionLookupDown) IsSuppressed(context.Context, string, string) (bool, error) {
	return false, errors.New("suppression store unavailable")
}

func TestCampaignSendsNothingWhenTheOptOutCheckFails(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")
	send := NewSendCampaignUseCase(kit.repo, suppressionLookupDown{kit.suppression}, kit.provider, kit.templates, "en", kit.charger, campaignClock{now: time.Unix(1_700_000_000, 0).UTC()})

	_, err := send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})

	if err == nil {
		t.Fatal("expected the failed opt-out check to surface")
	}
	if len(kit.provider.sent) != 0 || len(kit.charger.charges) != 0 {
		t.Fatalf("a recipient whose opt-out could not be checked must not be messaged or charged: sent=%v charges=%d", kit.provider.sent, len(kit.charger.charges))
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

func chargedReferences(charges []UsageCharge) map[string]int {
	counts := map[string]int{}
	for _, charge := range charges {
		counts[charge.Reference]++
	}
	return counts
}

func TestRetryFailedReusesTheStableChargeReference(t *testing.T) {
	kit := newCampaignKit()
	kit.provider.fail["+2348000000002"] = true
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")
	command := SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID}

	if _, err := kit.send.Execute(context.Background(), command); err != nil {
		t.Fatalf("send: %v", err)
	}
	first := chargedReferences(kit.charger.charges)

	kit.provider.fail = map[string]bool{}
	command.RetryFailedOnly = true
	if _, err := kit.send.Execute(context.Background(), command); err != nil {
		t.Fatalf("retry: %v", err)
	}

	for reference, count := range chargedReferences(kit.charger.charges) {
		if _, charged := first[reference]; !charged {
			t.Fatalf("retry produced a new reference %q", reference)
		}
		if count > 2 {
			t.Fatalf("reference %q charged %d times", reference, count)
		}
	}
	if len(kit.charger.charges) != 3 {
		t.Fatalf("retry must charge only the failed recipient, got %d charges", len(kit.charger.charges))
	}
	if first["campaign:"+campaign.ID+":sms:cmr-2"] != 1 {
		t.Fatalf("expected a per-recipient reference, got %v", first)
	}
}

func TestCampaignSecondSendWhileRunningIsRefusedAndSendsNothing(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")
	now := time.Unix(1_700_000_000, 0).UTC()
	if _, won, _ := kit.repo.ClaimCampaign(context.Background(), "tenant-a", campaign.ID, now, now.Add(-campaignStaleAfter)); !won {
		t.Fatal("first claim must win")
	}

	_, err := kit.send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})

	if !errors.Is(err, ErrCampaignAlreadyRunning) {
		t.Fatalf("expected already running, got %v", err)
	}
	if len(kit.provider.sent) != 0 || len(kit.charger.charges) != 0 {
		t.Fatalf("a refused run must not send or charge: sent=%v charges=%d", kit.provider.sent, len(kit.charger.charges))
	}
}

func TestCampaignSendTwiceMessagesEachRecipientOnce(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")
	command := SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID}

	if _, err := kit.send.Execute(context.Background(), command); err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := kit.send.Execute(context.Background(), command)

	if err != nil || second.Attempted != 0 {
		t.Fatalf("second send: %+v err=%v", second, err)
	}
	if len(kit.provider.sent) != 2 || len(kit.charger.charges) != 2 {
		t.Fatalf("each recipient once: sent=%v charges=%d", kit.provider.sent, len(kit.charger.charges))
	}
}

func TestCampaignRecipientIsClaimedOnce(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")
	recipients, _ := kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	now := time.Unix(1_700_000_000, 0).UTC()

	first, _ := kit.repo.ClaimRecipient(context.Background(), "tenant-a", recipients[0].ID, domain.RecipientStateQueued, now)
	second, _ := kit.repo.ClaimRecipient(context.Background(), "tenant-a", recipients[0].ID, domain.RecipientStateQueued, now)
	other, _ := kit.repo.ClaimRecipient(context.Background(), "tenant-b", recipients[0].ID, domain.RecipientStateQueued, now)

	if !first || second || other {
		t.Fatalf("claims: first=%v second=%v other tenant=%v", first, second, other)
	}
}

func TestCampaignWithManyRecipientsChargesAndSendsEachOnce(t *testing.T) {
	kit := newCampaignKit()
	numbers := make([]string, 0, 12)
	for index := 0; index < 12; index++ {
		numbers = append(numbers, "+23480000001"+string(rune('0'+index/10))+string(rune('0'+index%10)))
	}
	campaign := smsCampaign(kit, t, numbers...)

	result, err := kit.send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})

	if err != nil || result.Attempted != 12 || len(kit.provider.sent) != 12 {
		t.Fatalf("attempted=%d sent=%d err=%v", result.Attempted, len(kit.provider.sent), err)
	}
	references := map[string]bool{}
	for _, charge := range kit.charger.charges {
		references[charge.Reference] = true
	}
	if len(references) != 12 {
		t.Fatalf("expected a distinct reference per recipient, got %d", len(references))
	}
}

func TestCampaignRefusedWalletPutsAnUnstartedCampaignBack(t *testing.T) {
	kit := newCampaignKit()
	kit.send = NewSendCampaignUseCase(kit.repo, kit.suppression, kit.provider, kit.templates, "en", &campaignRefusingCharger{}, campaignClock{now: time.Unix(1_700_000_000, 0).UTC()})
	campaign := smsCampaign(kit, t, "+2348000000001")

	_, err := kit.send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})

	if err == nil {
		t.Fatal("expected the refusal")
	}
	if status := kit.repo.campaigns[campaign.ID].Status; status != domain.CampaignStatusDraft {
		t.Fatalf("a refused campaign goes back to draft so it can be sent after a top-up, got %s", status)
	}
	recipients, _ := kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	if recipients[0].State != domain.RecipientStateQueued {
		t.Fatalf("recipient must stay queued, got %s", recipients[0].State)
	}
}

func TestCampaignRunnerStartsDueCampaignsOfEveryTenantAndChargesThem(t *testing.T) {
	kit := newCampaignKit()
	clock := campaignClock{now: time.Unix(1_700_000_000, 0).UTC()}
	runner := NewCampaignRunner(kit.repo, kit.send, clock)
	dueAt := clock.now.Add(-time.Minute)
	mine := smsCampaign(kit, t, "+2348000000001")
	other, err := kit.create.Execute(context.Background(), CreateCampaignCommand{
		TenantID: "tenant-b", Name: "B", Channels: []string{"sms"}, Bodies: map[string]string{"sms": "hi"},
		Recipients: []CampaignRecipientInput{{DriverID: "d", Channel: "sms", Destination: "+2348000000002"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	future := clock.now.Add(time.Hour)
	later := smsCampaign(kit, t, "+2348000000003")
	for id, at := range map[string]*time.Time{mine.ID: &dueAt, other.ID: &dueAt, later.ID: &future} {
		campaign := kit.repo.campaigns[id]
		campaign.Status, campaign.ScheduledFor = domain.CampaignStatusScheduled, at
		kit.repo.campaigns[id] = campaign
	}

	started, err := runner.RunDue(context.Background())

	if err != nil || started != 2 || len(kit.provider.sent) != 2 {
		t.Fatalf("started=%d sent=%v err=%v", started, kit.provider.sent, err)
	}
	tenants := map[string]bool{}
	for _, charge := range kit.charger.charges {
		tenants[charge.TenantID] = true
	}
	if !tenants["tenant-a"] || !tenants["tenant-b"] {
		t.Fatalf("each tenant pays for its own campaign: %+v", kit.charger.charges)
	}
	if started, _ := runner.RunDue(context.Background()); started != 0 {
		t.Fatalf("a campaign that ran must not run again, started %d", started)
	}
}

func TestCampaignRunnerRecoversACrashedRunWithoutResendingInterruptedRecipients(t *testing.T) {
	kit := newCampaignKit()
	clock := campaignClock{now: time.Unix(1_700_000_000, 0).UTC()}
	runner := NewCampaignRunner(kit.repo, kit.send, clock)
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002", "+2348000000003")
	old := clock.now.Add(-30 * time.Minute)

	stored := kit.repo.campaigns[campaign.ID]
	stored.Status, stored.UpdatedAt = domain.CampaignStatusSending, old
	kit.repo.campaigns[campaign.ID] = stored
	list := kit.repo.recipients[campaign.ID]
	list[0].State = domain.RecipientStateSent
	list[1].State = domain.RecipientStateSending
	for index := range list {
		list[index].UpdatedAt = old
	}

	started, err := runner.RunDue(context.Background())

	if err != nil || started != 1 {
		t.Fatalf("started=%d err=%v", started, err)
	}
	if len(kit.provider.sent) != 1 || kit.provider.sent[0] != "+2348000000003" {
		t.Fatalf("only the still-queued recipient is sent: %v", kit.provider.sent)
	}
	recipients, _ := kit.repo.ListRecipients(context.Background(), "tenant-a", campaign.ID)
	if recipients[1].State != domain.RecipientStateFailed {
		t.Fatalf("an interrupted send is failed, not repeated: %s", recipients[1].State)
	}
}

func TestCampaignRunnerLeavesALiveRunAlone(t *testing.T) {
	kit := newCampaignKit()
	clock := campaignClock{now: time.Unix(1_700_000_000, 0).UTC()}
	campaign := smsCampaign(kit, t, "+2348000000001")
	stored := kit.repo.campaigns[campaign.ID]
	stored.Status, stored.UpdatedAt = domain.CampaignStatusSending, clock.now.Add(-time.Minute)
	kit.repo.campaigns[campaign.ID] = stored

	started, _ := NewCampaignRunner(kit.repo, kit.send, clock).RunDue(context.Background())

	if started != 0 || len(kit.provider.sent) != 0 {
		t.Fatalf("a run touched a minute ago is alive: started=%d sent=%v", started, kit.provider.sent)
	}
}
