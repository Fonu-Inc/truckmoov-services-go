package application

import (
	"context"
	"errors"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// DeliveryLogPage is one page of a tenant's log. Total is the count matching
// the filters, not the page, so a caller can page without a second request.
type DeliveryLogPage struct {
	Entries []domain.DeliveryLogEntry
	Total   int
	Limit   int
	Offset  int
}

const (
	defaultDeliveryLogLimit = 50
	maxDeliveryLogLimit     = 200
)

type ListDeliveryLogUseCase struct {
	deliveryLog DeliveryLogRepository
}

func NewListDeliveryLogUseCase(deliveryLog DeliveryLogRepository) *ListDeliveryLogUseCase {
	return &ListDeliveryLogUseCase{deliveryLog: deliveryLog}
}

func (u *ListDeliveryLogUseCase) Execute(
	ctx context.Context,
	tenantID string,
	filters domain.DeliveryLogFilters,
) (DeliveryLogPage, error) {
	if u == nil || u.deliveryLog == nil {
		return DeliveryLogPage{}, errors.New("delivery log is not configured")
	}
	if strings.TrimSpace(tenantID) == "" {
		return DeliveryLogPage{}, errors.New("tenant id is required")
	}

	// Clamped here rather than at the repository, so every caller of the use
	// case gets the same page size and one caller cannot ask for the whole log.
	normalised := filters
	normalised.NotificationType = strings.TrimSpace(filters.NotificationType)
	normalised.Channel = strings.TrimSpace(filters.Channel)
	normalised.Status = strings.TrimSpace(filters.Status)
	if normalised.Limit <= 0 || normalised.Limit > maxDeliveryLogLimit {
		normalised.Limit = defaultDeliveryLogLimit
	}
	if normalised.Offset < 0 {
		normalised.Offset = 0
	}

	entries, total, err := u.deliveryLog.List(ctx, strings.TrimSpace(tenantID), normalised)
	if err != nil {
		return DeliveryLogPage{}, err
	}

	return DeliveryLogPage{
		Entries: entries,
		Total:   total,
		Limit:   normalised.Limit,
		Offset:  normalised.Offset,
	}, nil
}
