package infrastructure

import (
	"context"
	"errors"
)

type MTNMessageProvider struct{}

func NewMTNMessageProvider() *MTNMessageProvider {
	return &MTNMessageProvider{}
}

func (p *MTNMessageProvider) Send(_ context.Context, channel, recipient, content string) (string, error) {
	if recipient == "" || content == "" {
		return "failed", errors.New("recipient and content are required")
	}
	if channel == "" {
		channel = "whatsapp"
	}
	return "sent", nil
}
