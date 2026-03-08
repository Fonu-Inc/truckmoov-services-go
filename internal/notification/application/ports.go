package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type OutboxRepository interface {
	Save(message domain.OutboxMessage) error
	FindByID(id string) (domain.OutboxMessage, bool)
}

type MessageProvider interface {
	Send(ctx context.Context, channel, recipient, content string) (status string, err error)
}

type DeliveryEventPublisher interface {
	PublishDeliveryUpdated(ctx context.Context, event domain.MessageDeliveryUpdated) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	New(prefix string) string
}
