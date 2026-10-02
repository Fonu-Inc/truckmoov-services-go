package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type refusingWallet struct {
	mu    sync.Mutex
	calls int
}

func (w *refusingWallet) Charge(context.Context, UsageCharge) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	return ErrInsufficientFunds
}

type pauseNotes struct{ paused []domain.Campaign }

func (n *pauseNotes) CampaignPaused(_ context.Context, campaign domain.Campaign) {
	n.paused = append(n.paused, campaign)
}

type failingTemplates struct{ calls int }

func (f *failingTemplates) SendTemplate(context.Context, domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error) {
	f.calls++
	return domain.WhatsAppSendResult{}, errors.New("provider rejected the template")
}

var campaignNow = time.Unix(1_700_000_000, 0).UTC()

func scheduledNow(kit *campaignKit, id string) {
	due := campaignNow.Add(-time.Minute)
	campaign := kit.repo.campaigns[id]
	campaign.Status, campaign.ScheduledFor = domain.CampaignStatusScheduled, &due
	kit.repo.campaigns[id] = campaign
}

func sendWith(kit *campaignKit, charger UsageCharger) *SendCampaignUseCase {
	return NewSendCampaignUseCase(kit.repo, kit.suppression, kit.provider, kit.templates, "en", charger, campaignClock{now: campaignNow})
}

func TestScheduledCampaignIsPausedAfterThreeRefusedRuns(t *testing.T) {
	kit := newCampaignKit()
	wallet := &refusingWallet{}
	notes := &pauseNotes{}
	kit.send = sendWith(kit, wallet).WithPauseNotifier(notes)
	runner := NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow})
	campaign := smsCampaign(kit, t, "+2348000000001")
	scheduledNow(kit, campaign.ID)

	for run, want := range []string{domain.CampaignStatusScheduled, domain.CampaignStatusScheduled, domain.CampaignStatusPausedNoFunds} {
		_, _ = runner.RunDue(context.Background())
		if status := kit.repo.campaigns[campaign.ID].Status; status != want {
			t.Fatalf("after run %d expected %s, got %s", run+1, want, status)
		}
	}

	callsAtPause := wallet.calls
	_, _ = runner.RunDue(context.Background())
	_, _ = runner.RunDue(context.Background())
	if wallet.calls != callsAtPause {
		t.Fatalf("a paused campaign must stop retrying each tick: %d calls then %d", callsAtPause, wallet.calls)
	}
	if len(notes.paused) != 1 || notes.paused[0].ID != campaign.ID {
		t.Fatalf("the tenant is told once, got %d notices", len(notes.paused))
	}
	if len(kit.provider.sent) != 0 {
		t.Fatalf("nothing may be sent while refused: %v", kit.provider.sent)
	}
}

func TestManualSendResumesAPausedCampaign(t *testing.T) {
	kit := newCampaignKit()
	kit.send = sendWith(kit, &refusingWallet{}).WithPauseNotifier(&pauseNotes{})
	runner := NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow})
	campaign := smsCampaign(kit, t, "+2348000000001")
	scheduledNow(kit, campaign.ID)
	for range maxCampaignRefusals {
		_, _ = runner.RunDue(context.Background())
	}
	if kit.repo.campaigns[campaign.ID].Status != domain.CampaignStatusPausedNoFunds {
		t.Fatal("setup: the campaign must be paused")
	}

	result, err := sendWith(kit, kit.charger).Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})

	if err != nil || result.Attempted != 1 || len(kit.provider.sent) != 1 {
		t.Fatalf("the manual send resumes it: attempted=%d sent=%v err=%v", result.Attempted, kit.provider.sent, err)
	}
	stored := kit.repo.campaigns[campaign.ID]
	if stored.Status == domain.CampaignStatusPausedNoFunds || stored.ConsecutiveRefusals != 0 {
		t.Fatalf("expected a resumed campaign with the count cleared, got %+v", stored)
	}
}

func TestManualRefusalDoesNotCountAndAPausedCampaignStaysPaused(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")
	manual := sendWith(kit, &refusingWallet{})

	for range maxCampaignRefusals + 1 {
		_, _ = manual.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})
	}

	stored := kit.repo.campaigns[campaign.ID]
	if stored.Status != domain.CampaignStatusDraft || stored.ConsecutiveRefusals != 0 {
		t.Fatalf("a person pressing Send is told at once and nothing is counted, got %+v", stored)
	}

	stored.Status = domain.CampaignStatusPausedNoFunds
	kit.repo.campaigns[campaign.ID] = stored
	_, _ = manual.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID})
	if status := kit.repo.campaigns[campaign.ID].Status; status != domain.CampaignStatusPausedNoFunds {
		t.Fatalf("a refused resume leaves it paused, got %s", status)
	}
}

func TestARunThatSendsSomethingClearsTheRefusalCount(t *testing.T) {
	kit := newCampaignKit()
	runner := NewCampaignRunner(kit.repo, sendWith(kit, &refusingWallet{}), campaignClock{now: campaignNow})
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")
	scheduledNow(kit, campaign.ID)
	_, _ = runner.RunDue(context.Background())
	_, _ = runner.RunDue(context.Background())
	if kit.repo.campaigns[campaign.ID].ConsecutiveRefusals != 2 {
		t.Fatalf("setup: expected two refusals, got %d", kit.repo.campaigns[campaign.ID].ConsecutiveRefusals)
	}

	runner = NewCampaignRunner(kit.repo, sendWith(kit, &campaignLimitedCharger{allowed: 1}), campaignClock{now: campaignNow})
	_, _ = runner.RunDue(context.Background())

	stored := kit.repo.campaigns[campaign.ID]
	if stored.ConsecutiveRefusals != 0 || stored.Status != domain.CampaignStatusScheduled {
		t.Fatalf("progress clears the count and keeps it scheduled, got %+v", stored)
	}
}

func TestPausedCampaignIsNotDue(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")
	scheduledNow(kit, campaign.ID)
	stored := kit.repo.campaigns[campaign.ID]
	stored.Status = domain.CampaignStatusPausedNoFunds
	kit.repo.campaigns[campaign.ID] = stored

	due, _ := kit.repo.ListDueCampaigns(context.Background(), campaignNow, campaignNow.Add(-campaignStaleAfter), 10)

	if len(due) != 0 {
		t.Fatalf("a paused campaign waits for a manual send, got %+v", due)
	}
}

type statusRacingRepository struct {
	*fakeCampaignRepository
	raceStatus string
	raced      bool
}

func (r *statusRacingRepository) SaveCampaignIfStatus(ctx context.Context, campaign domain.Campaign, expected string) (bool, error) {
	if !r.raced {
		r.raced = true
		r.mu.Lock()
		stored := r.campaigns[campaign.ID]
		stored.Status = r.raceStatus
		r.campaigns[campaign.ID] = stored
		r.mu.Unlock()
	}
	return r.fakeCampaignRepository.SaveCampaignIfStatus(ctx, campaign, expected)
}

func failedSMSCampaign(kit *campaignKit, t *testing.T) domain.Campaign {
	t.Helper()
	kit.provider.fail["+2348000000002"] = true
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")
	if _, err := kit.send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID}); err != nil {
		t.Fatalf("send: %v", err)
	}
	kit.provider.fail = map[string]bool{}
	return campaign
}

func TestRetryRunDoesNotOverwriteAStatusAnotherRunDecided(t *testing.T) {
	kit := newCampaignKit()
	campaign := failedSMSCampaign(kit, t)
	racing := &statusRacingRepository{fakeCampaignRepository: kit.repo, raceStatus: domain.CampaignStatusPausedNoFunds}
	retry := NewSendCampaignUseCase(racing, kit.suppression, kit.provider, kit.templates, "en", kit.charger, campaignClock{now: campaignNow})

	result, err := retry.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID, RetryFailedOnly: true})

	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if status := kit.repo.campaigns[campaign.ID].Status; status != domain.CampaignStatusPausedNoFunds || result.Campaign.Status != domain.CampaignStatusPausedNoFunds {
		t.Fatalf("the other run's status must stand, stored=%s result=%s", status, result.Campaign.Status)
	}
}

func TestRetryRunReloadsAndSettlesFromTheRecipientsWhenTheStatusMoved(t *testing.T) {
	kit := newCampaignKit()
	campaign := failedSMSCampaign(kit, t)
	racing := &statusRacingRepository{fakeCampaignRepository: kit.repo, raceStatus: domain.CampaignStatusFailed}
	retry := NewSendCampaignUseCase(racing, kit.suppression, kit.provider, kit.templates, "en", kit.charger, campaignClock{now: campaignNow})

	if _, err := retry.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID, RetryFailedOnly: true}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if status := kit.repo.campaigns[campaign.ID].Status; status != domain.CampaignStatusSending {
		t.Fatalf("expected the status derived from the recipients as they are now, got %s", status)
	}
}

func TestReceiptDoesNotOverwritePausedOrCancelledCampaigns(t *testing.T) {
	for _, status := range []string{domain.CampaignStatusPausedNoFunds, domain.CampaignStatusCancelled} {
		kit := newCampaignKit()
		campaign := smsCampaign(kit, t, "+2348000000001")
		list := kit.repo.recipients[campaign.ID]
		list[0].State, list[0].ProviderMessageID = domain.RecipientStateSent, "wamid.r1"
		stored := kit.repo.campaigns[campaign.ID]
		stored.Status = status
		kit.repo.campaigns[campaign.ID] = stored

		if _, err := kit.receipt.Execute(context.Background(), "wamid.r1", domain.RecipientStateDelivered, ""); err != nil {
			t.Fatalf("receipt: %v", err)
		}

		if got := kit.repo.campaigns[campaign.ID].Status; got != status {
			t.Fatalf("a receipt must not change %s, got %s", status, got)
		}
	}
}

func whatsAppCampaign(kit *campaignKit, t *testing.T, numbers ...string) domain.Campaign {
	t.Helper()
	recipients := make([]CampaignRecipientInput, 0, len(numbers))
	for index, number := range numbers {
		recipients = append(recipients, CampaignRecipientInput{DriverID: "driver-" + string(rune('a'+index)), Channel: "whatsapp", Destination: number})
	}
	campaign, err := kit.create.Execute(context.Background(), CreateCampaignCommand{
		TenantID: "tenant-a", Name: "Notice", Channels: []string{"whatsapp"}, Bodies: map[string]string{"whatsapp": "hi"},
		TemplateName: "notice_v1", Recipients: recipients,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	return campaign
}

func interrupt(kit *campaignKit, campaignID string, age time.Duration, indexes ...int) {
	stored := kit.repo.campaigns[campaignID]
	stored.Status = domain.CampaignStatusFailed
	kit.repo.campaigns[campaignID] = stored
	list := kit.repo.recipients[campaignID]
	for _, index := range indexes {
		list[index].State, list[index].Interrupted = domain.RecipientStateFailed, true
		list[index].UpdatedAt = campaignNow.Add(-age)
	}
}

func TestInterruptedRecipientIsRetriedOnceAfterFifteenMinutes(t *testing.T) {
	kit := newCampaignKit()
	runner := NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow})
	campaign := whatsAppCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, 16*time.Minute, 0)

	retried, err := runner.RetryInterrupted(context.Background())

	if err != nil || retried != 1 || len(kit.templates.sent) != 1 {
		t.Fatalf("retried=%d sent=%v err=%v", retried, kit.templates.sent, err)
	}
	recipient := kit.repo.recipients[campaign.ID][0]
	if recipient.State != domain.RecipientStateSent || !recipient.AutoRetried || recipient.Interrupted {
		t.Fatalf("expected a sent recipient that used its one retry, got %+v", recipient)
	}
	if again, _ := runner.RetryInterrupted(context.Background()); again != 0 || len(kit.templates.sent) != 1 {
		t.Fatalf("it must not be sent a second time, got retried=%d sent=%v", again, kit.templates.sent)
	}
}

func TestInterruptedRecipientWaitsTheFullFifteenMinutes(t *testing.T) {
	kit := newCampaignKit()
	campaign := whatsAppCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, 5*time.Minute, 0)

	retried, _ := NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow}).RetryInterrupted(context.Background())

	if retried != 0 || len(kit.templates.sent) != 0 {
		t.Fatalf("too early to retry, got retried=%d sent=%v", retried, kit.templates.sent)
	}
}

func TestInterruptedRecipientOnAChannelWithoutMessageIDsStaysFailed(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, time.Hour, 0)

	retried, _ := NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow}).RetryInterrupted(context.Background())

	if retried != 0 || len(kit.provider.sent) != 0 {
		t.Fatalf("an sms that may have left is not sent again on its own, got retried=%d sent=%v", retried, kit.provider.sent)
	}
	if kit.repo.recipients[campaign.ID][0].State != domain.RecipientStateFailed {
		t.Fatal("it stays failed for an operator")
	}
}

func TestInterruptedRecipientWithAProviderMessageIDStaysFailed(t *testing.T) {
	kit := newCampaignKit()
	campaign := whatsAppCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, time.Hour, 0)
	kit.repo.recipients[campaign.ID][0].ProviderMessageID = "wamid.known"

	retried, _ := NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow}).RetryInterrupted(context.Background())

	if retried != 0 || len(kit.templates.sent) != 0 {
		t.Fatalf("the provider named a message, so it is not repeated, got retried=%d", retried)
	}
}

func TestInterruptedRecipientThatFailsAgainStaysFailedAfterItsOneRetry(t *testing.T) {
	kit := newCampaignKit()
	failing := &failingTemplates{}
	send := NewSendCampaignUseCase(kit.repo, kit.suppression, kit.provider, failing, "en", kit.charger, campaignClock{now: campaignNow})
	runner := NewCampaignRunner(kit.repo, send, campaignClock{now: campaignNow})
	campaign := whatsAppCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, time.Hour, 0)

	_, _ = runner.RetryInterrupted(context.Background())
	_, _ = runner.RetryInterrupted(context.Background())

	recipient := kit.repo.recipients[campaign.ID][0]
	if failing.calls != 1 || recipient.State != domain.RecipientStateFailed || !recipient.AutoRetried {
		t.Fatalf("one retry only, calls=%d recipient=%+v", failing.calls, recipient)
	}
}

func TestAutomaticRetryOfAnInterruptedRecipientIsChargedOnTheSameReference(t *testing.T) {
	kit := newCampaignKit()
	campaign := whatsAppCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, time.Hour, 0)
	recipientID := kit.repo.recipients[campaign.ID][0].ID

	_, _ = NewCampaignRunner(kit.repo, kit.send, campaignClock{now: campaignNow}).RetryInterrupted(context.Background())

	if len(kit.charger.charges) != 1 || kit.charger.charges[0].Reference != "campaign:"+campaign.ID+":whatsapp:"+recipientID {
		t.Fatalf("the stable reference makes a second charge a no-op, got %+v", kit.charger.charges)
	}
}

func TestAutomaticRetryRefusedByTheWalletIsNotGivenAnotherTry(t *testing.T) {
	kit := newCampaignKit()
	send := NewSendCampaignUseCase(kit.repo, kit.suppression, kit.provider, kit.templates, "en", &refusingWallet{}, campaignClock{now: campaignNow})
	campaign := whatsAppCampaign(kit, t, "+2348000000001")
	interrupt(kit, campaign.ID, time.Hour, 0)

	_, _ = NewCampaignRunner(kit.repo, send, campaignClock{now: campaignNow}).RetryInterrupted(context.Background())

	recipient := kit.repo.recipients[campaign.ID][0]
	if recipient.State != domain.RecipientStateFailed || !recipient.AutoRetried {
		t.Fatalf("a refused wallet leaves it failed, for an operator, got %+v", recipient)
	}
}

func TestRetryInterruptedActionResendsOnlyToInterruptedRecipients(t *testing.T) {
	kit := newCampaignKit()
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002", "+2348000000003")
	list := kit.repo.recipients[campaign.ID]
	list[0].State = domain.RecipientStateFailed
	list[1].State, list[1].Interrupted = domain.RecipientStateFailed, true
	list[2].State = domain.RecipientStateSent
	stored := kit.repo.campaigns[campaign.ID]
	stored.Status = domain.CampaignStatusSending
	kit.repo.campaigns[campaign.ID] = stored

	result, err := kit.send.Execute(context.Background(), SendCampaignCommand{TenantID: "tenant-a", CampaignID: campaign.ID, InterruptedOnly: true})

	if err != nil || result.Attempted != 1 || len(kit.provider.sent) != 1 || kit.provider.sent[0] != "+2348000000002" {
		t.Fatalf("expected only the interrupted recipient, attempted=%d sent=%v err=%v", result.Attempted, kit.provider.sent, err)
	}
	if kit.repo.recipients[campaign.ID][0].State != domain.RecipientStateFailed {
		t.Fatal("an ordinary failure is left for the plain retry")
	}
}

func TestCrashRecoveryMarksTheRecipientsItFailedAsInterrupted(t *testing.T) {
	kit := newCampaignKit()
	clock := campaignClock{now: campaignNow}
	campaign := smsCampaign(kit, t, "+2348000000001", "+2348000000002")
	old := campaignNow.Add(-30 * time.Minute)
	stored := kit.repo.campaigns[campaign.ID]
	stored.Status, stored.UpdatedAt = domain.CampaignStatusSending, old
	kit.repo.campaigns[campaign.ID] = stored
	list := kit.repo.recipients[campaign.ID]
	list[0].State = domain.RecipientStateSending
	for index := range list {
		list[index].UpdatedAt = old
	}

	_, _ = NewCampaignRunner(kit.repo, kit.send, clock).RunDue(context.Background())

	if recipient := kit.repo.recipients[campaign.ID][0]; recipient.State != domain.RecipientStateFailed || !recipient.Interrupted {
		t.Fatalf("expected a failed, interrupted recipient, got %+v", recipient)
	}
}
