package infrastructure

import (
	"context"

	"github.com/truckmoov/services-go/internal/notification/application"
)

var _ application.VoiceProvider = NullVoiceProvider{}

// NullVoiceProvider stands in until a voice carrier is connected. It never
// places a call and says so, instead of pretending to.
type NullVoiceProvider struct{}

func (NullVoiceProvider) Name() string { return "none" }

func (NullVoiceProvider) Configured() bool { return false }

func (NullVoiceProvider) InitiateCall(context.Context, application.VoiceCallRequest) (string, error) {
	return "", application.ErrVoiceNotConfigured
}
