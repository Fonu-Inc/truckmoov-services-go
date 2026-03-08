package infrastructure

import (
	"sync"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type InMemoryOutboxRepository struct {
	mu   sync.RWMutex
	data map[string]domain.OutboxMessage
}

func NewInMemoryOutboxRepository() *InMemoryOutboxRepository {
	return &InMemoryOutboxRepository{data: make(map[string]domain.OutboxMessage)}
}

func (r *InMemoryOutboxRepository) Save(message domain.OutboxMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[message.ID] = message
	return nil
}

func (r *InMemoryOutboxRepository) FindByID(id string) (domain.OutboxMessage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	message, ok := r.data[id]
	return message, ok
}
