package infrastructure

import (
	"context"
	"errors"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/application"
)

func TestChargerRefusesWhenNotConfigured(t *testing.T) {
	for name, charger := range map[string]*AuthUsageCharger{
		"no url":    NewAuthUsageCharger("", nil, nil),
		"no caller": NewAuthUsageCharger("http://auth", nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			err := charger.Charge(context.Background(), application.UsageCharge{TenantID: "t1", Channel: "sms", Quantity: 1})
			if !errors.Is(err, ErrChargerNotConfigured) {
				t.Fatalf("expected ErrChargerNotConfigured, got %v", err)
			}
		})
	}
}
