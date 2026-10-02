package application

import (
	"context"
	"strings"

	"github.com/truckmoov/services-go/pkg/identity"
)

type LinkedDriver struct {
	DriverID string `json:"driver_id"`
	Name     string `json:"name"`
	TenantID string `json:"tenant_id"`
	Status   string `json:"status"`
}

type ContactLinks struct {
	Drivers []LinkedDriver `json:"drivers"`
}

// DriverLookup finds the drivers who list a phone number.
type DriverLookup interface {
	DriversByPhone(ctx context.Context, phone string) ([]LinkedDriver, error)
}

// PlatformContactLinks ties a conversation's number to the people and
// tenants that own it, so staff know who they are talking to.
type PlatformContactLinks struct {
	repository PlatformMessagingRepository
	drivers    DriverLookup
}

func NewPlatformContactLinks(repository PlatformMessagingRepository, drivers DriverLookup) *PlatformContactLinks {
	return &PlatformContactLinks{repository: repository, drivers: drivers}
}

func (u *PlatformContactLinks) Execute(ctx context.Context, conversationID string) (ContactLinks, error) {
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, strings.TrimSpace(conversationID))
	if err != nil {
		return ContactLinks{}, err
	}
	if !ok {
		return ContactLinks{}, ErrWhatsAppConversationNotFound
	}
	drivers, err := u.drivers.DriversByPhone(ctx, conversation.DriverPhone)
	if err != nil {
		return ContactLinks{}, err
	}
	if drivers == nil {
		drivers = []LinkedDriver{}
	}
	return ContactLinks{Drivers: drivers}, nil
}
