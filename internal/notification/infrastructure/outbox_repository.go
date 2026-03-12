package infrastructure

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/truckmoov/services-go/internal/notification/domain"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
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

type RedisOutboxRepository struct {
	store *redisstore.Store
}

func NewRedisOutboxRepository(store *redisstore.Store) *RedisOutboxRepository {
	return &RedisOutboxRepository{store: store}
}

func (r *RedisOutboxRepository) Save(message domain.OutboxMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal outbox message: %w", err)
	}
	return r.store.Set(redisOutboxKey(message.ID), payload)
}

func (r *RedisOutboxRepository) FindByID(id string) (domain.OutboxMessage, bool) {
	payload, ok, err := r.store.Get(redisOutboxKey(id))
	if err != nil || !ok {
		return domain.OutboxMessage{}, false
	}

	var message domain.OutboxMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return domain.OutboxMessage{}, false
	}
	return message, true
}

func redisOutboxKey(id string) string {
	return "notification:outbox:" + id
}
