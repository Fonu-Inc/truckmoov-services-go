package application

import (
	"context"
	"log/slog"
	"strings"
)

// AccountRefresher re-reads a connected number after Meta reports a change.
type AccountRefresher interface {
	RefreshForBusinessAccount(ctx context.Context, businessAccountID string)
}

func (u *HandleWhatsAppWebhookUseCase) WithAccountRefresher(refresher AccountRefresher) *HandleWhatsAppWebhookUseCase {
	u.accountRefresher = refresher
	return u
}

func isAccountChangeField(field string) bool {
	switch strings.TrimSpace(field) {
	case "account_update", "phone_number_quality_update", "phone_number_name_update", "account_alerts", "account_review_update":
		return true
	}
	return false
}

func (u *HandleWhatsAppWebhookUseCase) refreshAccountOnChange(ctx context.Context, businessAccountID, field string) {
	if u.accountRefresher == nil || !isAccountChangeField(field) {
		return
	}
	u.accountRefresher.RefreshForBusinessAccount(ctx, businessAccountID)
}

// alreadyStored spots a webhook Meta delivered again, so it is not announced
// to staff a second time.
func (u *HandleWhatsAppWebhookUseCase) alreadyStored(ctx context.Context, providerMessageID string) bool {
	if strings.TrimSpace(providerMessageID) == "" {
		return false
	}
	_, found, err := u.repository.FindMessageByProviderMessageID(ctx, strings.TrimSpace(providerMessageID))
	return err == nil && found
}

// logDroppedInbound makes a message for a number nobody connected visible; it
// logs only the number id, never the message.
func (u *HandleWhatsAppWebhookUseCase) logDroppedInbound(ctx context.Context, phoneNumberID string) {
	if phoneNumberID == "" || u.accountRepository == nil {
		return
	}
	if _, found, err := u.accountRepository.FindAccountByPhoneNumberID(ctx, phoneNumberID); err == nil && !found {
		slog.Default().Warn("whatsapp_inbound_unknown_number", "phone_number_id", phoneNumberID)
	}
}
