package domain

import "testing"

func recipient(state, channel string) CampaignRecipient {
	return CampaignRecipient{State: state, Channel: channel}
}

func TestNewCampaignRequiresABodyPerChannel(t *testing.T) {
	_, err := NewCampaign(Campaign{
		ID: "c1", TenantID: "t1", Name: "Weekly update",
		Channels: []string{"sms", "email"},
		Bodies:   map[string]string{"sms": "short"},
	})
	if err == nil {
		t.Fatal("expected an error when a selected channel has no body")
	}
}

func TestNewCampaignRequiresATemplateForWhatsApp(t *testing.T) {
	_, err := NewCampaign(Campaign{
		ID: "c1", TenantID: "t1", Name: "Weekly update",
		Channels: []string{"whatsapp"},
		Bodies:   map[string]string{"whatsapp": "hello"},
	})
	if err == nil {
		t.Fatal("expected an error: business-initiated WhatsApp is template-gated")
	}
}

func TestNewCampaignRejectsAnUnknownChannel(t *testing.T) {
	if _, err := NewCampaign(Campaign{
		ID: "c1", TenantID: "t1", Name: "n",
		Channels: []string{"carrier-pigeon"},
		Bodies:   map[string]string{"carrier-pigeon": "hi"},
	}); err == nil {
		t.Fatal("expected an error for an unsupported channel")
	}
}

func TestNewCampaignDeduplicatesChannels(t *testing.T) {
	campaign, err := NewCampaign(Campaign{
		ID: "c1", TenantID: "t1", Name: "n",
		Channels: []string{"SMS", "sms"},
		Bodies:   map[string]string{"sms": "hi"},
	})
	if err != nil {
		t.Fatalf("build campaign: %v", err)
	}
	if len(campaign.Channels) != 1 {
		t.Fatalf("expected one channel, got %v", campaign.Channels)
	}
}

func TestOnlyWhatsAppReportsRead(t *testing.T) {
	if !ChannelReportsRead("whatsapp") {
		t.Fatal("whatsapp reports reads")
	}
	// Counting reads against SMS would report every campaign as unread.
	if ChannelReportsRead("sms") || ChannelReportsRead("email") {
		t.Fatal("sms and email do not report reads")
	}
}

func TestDeliveredIsTerminalOnlyWhereReadsNeverCome(t *testing.T) {
	if recipient(RecipientStateDelivered, "whatsapp").IsTerminal() {
		t.Fatal("a delivered whatsapp message may still be read")
	}
	if !recipient(RecipientStateDelivered, "sms").IsTerminal() {
		t.Fatal("a delivered sms will never report anything further")
	}
}

func TestOnlyAFailureMayBeRetried(t *testing.T) {
	if !recipient(RecipientStateFailed, "sms").CanRetry() {
		t.Fatal("a failure may be retried")
	}
	// Retrying someone who received it sends twice and charges twice.
	for _, state := range []string{RecipientStateSent, RecipientStateDelivered, RecipientStateRead} {
		if recipient(state, "whatsapp").CanRetry() {
			t.Fatalf("%s must not be retried", state)
		}
	}
}

func TestSummarizeNestsTheCounts(t *testing.T) {
	progress := SummarizeRecipients([]CampaignRecipient{
		recipient(RecipientStateRead, "whatsapp"),
		recipient(RecipientStateDelivered, "whatsapp"),
		recipient(RecipientStateSent, "sms"),
		recipient(RecipientStateFailed, "sms"),
	})

	// A read message was delivered and a delivered one was sent, so the counts
	// nest. Otherwise the delivery rate reads lower than it actually was.
	if progress.Sent != 3 || progress.Delivered != 2 || progress.Read != 1 || progress.Failed != 1 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	if progress.Recipients != 4 {
		t.Fatalf("expected 4 recipients, got %d", progress.Recipients)
	}
}

func TestStatusStaysSendingUntilEveryRecipientSettles(t *testing.T) {
	status := ResolveCampaignStatus([]CampaignRecipient{
		recipient(RecipientStateDelivered, "sms"),
		recipient(RecipientStateQueued, "sms"),
	})
	if status != CampaignStatusSending {
		t.Fatalf("expected sending, got %s", status)
	}
}

func TestStatusCompletesWhenSomeSucceeded(t *testing.T) {
	status := ResolveCampaignStatus([]CampaignRecipient{
		recipient(RecipientStateDelivered, "sms"),
		recipient(RecipientStateFailed, "sms"),
	})
	// Partly delivered is completed, not failed. Some drivers got the message.
	if status != CampaignStatusCompleted {
		t.Fatalf("expected completed, got %s", status)
	}
}

func TestStatusFailsOnlyWhenNobodyGotIt(t *testing.T) {
	status := ResolveCampaignStatus([]CampaignRecipient{
		recipient(RecipientStateFailed, "sms"),
		recipient(RecipientStateSuppressed, "sms"),
	})
	if status != CampaignStatusFailed {
		t.Fatalf("expected failed, got %s", status)
	}
}

func TestAWhatsAppDeliveryKeepsTheCampaignSending(t *testing.T) {
	// The read receipt is still outstanding, so the campaign has not settled.
	status := ResolveCampaignStatus([]CampaignRecipient{recipient(RecipientStateDelivered, "whatsapp")})
	if status != CampaignStatusSending {
		t.Fatalf("expected sending, got %s", status)
	}
}
